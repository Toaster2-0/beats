// read the README.md of the root folder

package sqlout

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/go-sql-driver/mysql"
)

const quoteFormat = "`%v`"
const valueQuoteFormat = "'%v',"

var charsToEscape = map[string]string{"'": "'", `\`: `\`}

const createTableQuery = `CREATE TABLE IF NOT EXISTS ` + quoteFormat + `(
	id BIGINT AUTO_INCREMENT PRIMARY KEY
 ) ROW_FORMAT=DYNAMIC;`

const alterTableQuery = `ALTER TABLE ` + quoteFormat + ` ADD ` + quoteFormat + ` %v%v;`

const insertQuery = `INSERT INTO ` + quoteFormat + ` (%v) VALUES %v;`

const foreignKeyConstraintFormat = `, ADD CONSTRAINT FOREIGN KEY(` + quoteFormat + `) REFERENCES ` + quoteFormat + `(id) ON DELETE CASCADE`

var getColumnNameFromErrorRegex = regexp.MustCompile(`'[^']+'`)

func (client *Client) flattenEvents(events []publisher.Event) map[string][]any {

	flattenedMap := map[string][]any{}
	flattenedMap["timestamp"] = make([]any, len(events))
	for index, event := range events {
		for key, value := range event.Content.Fields.Flatten() {
			if flattenedMap[key] == nil {
				flattenedMap[key] = make([]any, len(events))
			}
			flattenedMap[key][index] = value
		}
		flattenedMap["timestamp"][index] = event.Content.Timestamp
	}

	return flattenedMap
}

func (client *Client) createInsertQuery(data *[]map[string]map[string][]any, executionIndex int, tableToCreateQueryFor string, previousInsertedFirstIds map[string]int64) string {

	columnNames := ""
	var values []string
	for columnName, columnData := range (*data)[executionIndex][tableToCreateQueryFor] {

		//columnname can only be 64 chars long TODO mention in Docu
		if len(columnName) > 64 {
			(*data)[executionIndex][tableToCreateQueryFor][columnName[len(columnName)-64:]] = columnData
			delete((*data)[executionIndex][tableToCreateQueryFor], columnName)
			continue
		}

		columnDataType := getDataType(columnData)
		// TODO make for columnDataType Struct
		if columnDataType == "Array" {

			// Tablename may not be longer than 64 chars TODO mention in Docu
			var foreignTableName string
			if len(tableToCreateQueryFor) > 40 {
				foreignTableName = tableToCreateQueryFor[len(tableToCreateQueryFor)-40:]
			} else {
				foreignTableName = tableToCreateQueryFor
			}
			if len(columnName) > 23 {
				foreignTableName += "_" + columnName[len(columnName)-23:]
			} else {
				foreignTableName += "_" + columnName
			}

			if len((*data)) <= executionIndex+1 {
				*data = append((*data), map[string]map[string][]any{})
			}
			if (*data)[executionIndex+1][foreignTableName] == nil {
				(*data)[executionIndex+1][foreignTableName] = map[string][]any{}
			}
			if (*data)[executionIndex+1][foreignTableName]["fk_"+tableToCreateQueryFor] == nil {
				(*data)[executionIndex+1][foreignTableName]["fk_"+tableToCreateQueryFor] = []any{}
			}
			if (*data)[executionIndex+1][foreignTableName][columnName] == nil {
				(*data)[executionIndex+1][foreignTableName][columnName] = []any{}
			}
			for relativeId := range (*data)[executionIndex][tableToCreateQueryFor][columnName] {

				if (*data)[executionIndex][tableToCreateQueryFor][columnName][relativeId] == nil {
					continue
				}

				column := reflect.ValueOf((*data)[executionIndex][tableToCreateQueryFor][columnName][relativeId])
				for i := 0; i < column.Len(); i++ {
					(*data)[executionIndex+1][foreignTableName]["fk_"+tableToCreateQueryFor] = append((*data)[executionIndex+1][foreignTableName]["fk_"+tableToCreateQueryFor], int64(relativeId))
					(*data)[executionIndex+1][foreignTableName][columnName] = append((*data)[executionIndex+1][foreignTableName][columnName], column.Index(i).Interface())
				}
			}
			delete((*data)[executionIndex][tableToCreateQueryFor], columnName)
			continue
		}
		if columnDataType == "" {
			client.log.Errorf("failed to get data type from values: %v", columnData)
			continue
		}

		columnNames += fmt.Sprintf(quoteFormat, columnName) + ","

		var foreignKeyFirstId int64
		if strings.HasPrefix(columnName, "fk_") {
			foreignKeyFirstId = previousInsertedFirstIds[columnName[3:]]
		}

		for rowIndex, columnRowData := range columnData {
			if len(values) <= rowIndex {
				values = append(values, "(")
			}

			if columnRowData == nil {
				values[rowIndex] += "NULL,"
				continue
			}

			switch columnDataType {
			case "TEXT":
				valueString := columnRowData.(string)
				for char, escapedChar := range charsToEscape {
					if strings.Contains(valueString, char) {
						columnRowData = strings.Replace(valueString, char, escapedChar, -1)
					}
				}
				values[rowIndex] += fmt.Sprintf(valueQuoteFormat, columnRowData)

			case "BIGINT", "FLOAT":
				if foreignKeyFirstId != 0 {
					columnRowData = columnRowData.(int64) + foreignKeyFirstId
				}

				values[rowIndex] += fmt.Sprintf("%v,", columnRowData)

			case "TIMESTAMP":
				time := columnRowData.(time.Time).Format("2006-01-02 15:04:05")
				values[rowIndex] += fmt.Sprintf(valueQuoteFormat, time)

			}
		}
	}

	valuesString := ""
	//removes last comma
	columnNames = columnNames[:len(columnNames)-1]
	for rowIndex, value := range values {
		value = value[:len(value)-1]
		value += ")"
		if rowIndex != len(values)-1 {
			value += ","
		}
		valuesString += value
	}
	return fmt.Sprintf(insertQuery, tableToCreateQueryFor, columnNames, valuesString)
}

func (client *Client) insertData(ctx context.Context, data *[]map[string]map[string][]any) error {
	transaction, err := client.conn.BeginTx(ctx, nil)
	defer transaction.Rollback()
	if err != nil {
		client.log.Errorf("cannot start transaction: %v", err)
		return err
	}
	previouslyInsertedIds := map[string]int64{}
	for executionIndex := 0; executionIndex < len(*data); executionIndex++ {
		lastInsertedIds := map[string]int64{}
		for tablename := range (*data)[executionIndex] {
			res, err := client.executeInsertQuery(ctx,
				client.createInsertQuery(data, executionIndex, tablename, previouslyInsertedIds),
				(*data)[executionIndex][tablename], transaction, tablename)
			if err != nil {
				return err
			}

			lastInsertedId, err := res.LastInsertId()
			if err != nil {
				client.log.Errorf("cannot read last inserted id: %v", err)
				return err
			}
			// affectedRows, err := res.RowsAffected()
			// if err != nil {
			// 	client.log.Errorf("cannot read affected rows: %v")
			// 	return err
			// }
			// client.currentTransact = append(client.currentTransact, strconv.FormatInt(lastInsertedId, 10)+"/"+strconv.FormatInt(affectedRows, 10))
			// //first insertedId
			//lastInsertedId is first inserted id of insert query
			lastInsertedIds[tablename] = lastInsertedId //- affectedRows
		}
		previouslyInsertedIds = lastInsertedIds
	}
	err = transaction.Commit()
	if err != nil {
		client.log.Errorf("cannot commit transaction: %v")
		return err
	}
	return nil

}

func (client *Client) executeInsertQuery(ctx context.Context, insertQuery string, data map[string][]any, transaction *sql.Tx, tablename string) (sql.Result, error) {
	deadline := time.Now().Add(10000 * time.Millisecond)
	insertCtx, cancelCtx := context.WithDeadline(ctx, deadline)
	defer cancelCtx()
	// client.currentTransact = append(client.currentTransact, insertQuery)
	res, err := transaction.ExecContext(insertCtx, insertQuery)
	if err != nil {
		sqlErr := err.(*mysql.MySQLError)

		// Column does not exist
		if sqlErr.Number == 1054 {
			//TODO problem if columnName is with ' (getting columnName from error message)
			columnName := strings.ReplaceAll(getColumnNameFromErrorRegex.FindString(sqlErr.Message), "'", "")
			addColumnErr := client.addColumn(ctx, tablename, columnName, data[columnName])
			if addColumnErr != nil {
				return nil, addColumnErr
			}
			return client.executeInsertQuery(ctx, insertQuery, data, transaction, tablename)
		}
		// Table does not exist
		if sqlErr.Number == 1146 {
			createTableErr := client.createTable(ctx, tablename)
			if createTableErr != nil {
				return nil, createTableErr
			}
			return client.executeInsertQuery(ctx, insertQuery, data, transaction, tablename)
		}
		client.log.Errorf("error: %v while executing sql query: %v", err, insertQuery)
		// fmt.Println(client.currentTransact)
		return nil, err

	}
	return res, nil
}

func (client *Client) addColumn(ctx context.Context, tablename string, columnName string, exampleValuesForType []any) error {

	sqlType := getDataType(exampleValuesForType)
	if sqlType == "" {
		client.log.Errorf("failed to get SQL type from values: %v", exampleValuesForType)
		return fmt.Errorf("failed to get SQL type from values: %v", exampleValuesForType)
	}
	if sqlType == "Array" {
		client.log.Errorf("cannot create field for Array: %v", exampleValuesForType)
		return fmt.Errorf("cannot create field for Array: %v", exampleValuesForType)
	}
	foreignKeyConstraint := ""
	if strings.HasPrefix(columnName, "fk_") {
		foreignKeyConstraint = fmt.Sprintf(foreignKeyConstraintFormat, columnName, columnName[3:])
	}
	alterTable := fmt.Sprintf(alterTableQuery, tablename, columnName, sqlType, foreignKeyConstraint)
	deadline := time.Now().Add(5000 * time.Millisecond)
	alterctx, cancelCtx := context.WithDeadline(ctx, deadline)
	defer cancelCtx()
	_, err := client.conn.ExecContext(alterctx, alterTable)
	if err != nil {
		client.log.Errorf("error: %v while creating column: %v", err, fmt.Sprintf(alterTableQuery, tablename, columnName, sqlType, foreignKeyConstraint))
		return err
	}
	fmt.Print("added col")
	return nil

}

func (client *Client) createTable(ctx context.Context, tablename string) error {

	createTable := fmt.Sprintf(createTableQuery, tablename)
	_, err := client.conn.ExecContext(ctx, createTable)
	if err != nil {
		client.log.Errorf("error: %v while creating Table: %v", err, fmt.Sprintf(createTable, tablename))
	}
	return nil

}

func getDataType(dataArray []any) string {
	for _, value := range dataArray {

		switch value.(type) {
		case string:
			return "TEXT"
		case int, int8, int32, int64, uint, uint8, uint16, uint32, uint64:
			return "BIGINT"
		case float64, float32:
			return "FLOAT"
		case time.Time:
			return "TIMESTAMP"
		default:
			if value == nil {
				continue
			}
			if valueType := reflect.TypeOf(value).Kind(); valueType == reflect.Slice || valueType == reflect.Array {
				return "Array"
			}
		}
	}
	return ""
}
