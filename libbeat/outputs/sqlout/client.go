// read the README.md of the root folder

package sqlout

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/elastic/beats/v7/libbeat/outputs"
	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/elastic/elastic-agent-libs/logp"
	"github.com/go-sql-driver/mysql"
)

// Client is an elasticsearch client.
type Client struct {
	conn   *sql.DB
	config sqlConfig
	host   string

	observer outputs.Observer

	log *logp.Logger
}

func CreateSqlConfig(user string, password string, dbname string, host string, protocol string) *mysql.Config {
	config := mysql.NewConfig()
	config.User = user
	config.Passwd = password
	config.DBName = dbname
	config.Addr = host
	config.Net = protocol
	return config
}

// NewClient instantiates a new client.
func NewClient(
	config sqlConfig,
	host string,
	observer outputs.Observer,
) *Client {
	client := &Client{config: config, host: host, observer: observer, log: logp.NewLogger("sqlout")}

	return client
}

func NewConn(
	config sqlConfig,
	host string) (*sql.DB, error) {
	var protocol string
	if strings.HasPrefix(host, "/") {
		protocol = "unix"
	} else {
		protocol = "tcp"
	}
	sqlconfig := CreateSqlConfig(config.Username, config.Password, config.DBName, host, protocol)

	return sql.Open("mysql", sqlconfig.FormatDSN())
}

func (client *Client) Connect() error {
	conn, err := NewConn(client.config, client.host)
	if err != nil {
		client.log.Errorf("creating conn err:", err)
		return err
	}
	client.conn = conn
	// client.createTable(context.TODO(), "test")
	// res, _ := client.conn.ExecContext(context.TODO(), "INSERT INTO test values (), ();")
	// lastInsertedId, err := res.LastInsertId()
	// if err != nil {
	// 	client.log.Errorf("cannot read last inserted id: %v", err)
	// 	return err
	// }
	// affectedRows, err := res.RowsAffected()
	// if err != nil {
	// 	client.log.Errorf("cannot read affected rows: %v")
	// 	return err
	// }
	// fmt.Print(lastInsertedId, "§", affectedRows)
	_, err = client.conn.ExecContext(context.TODO(), fmt.Sprintf(createTableQuery, client.config.TablenameRoot))
	if err != nil {
		client.log.Errorf("connect err:", err)
		return err
	}
	return err
}

func (client *Client) Publish(ctx context.Context, batch publisher.Batch) error {
	// client.currentTransact = make([]string, 0)
	st := client.observer
	events := batch.Events()
	st.NewBatch(len(events))

	insertData := []map[string]map[string][]any{{}}
	insertData[0][client.config.TablenameRoot] = client.flattenEvents(events)

	err := client.insertData(ctx, &insertData)
	if err != nil {
		batch.RetryEvents(events)
		return err
	}

	client.observer.AckedEvents(len(events))
	batch.ACK()

	return nil
}

// Implement Outputer
func (client *Client) Close() error {
	return client.conn.Close()
}

func (client *Client) String() string {
	return "sql(" + client.host + ")"
}
