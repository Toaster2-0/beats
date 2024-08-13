// read the README.md of the root folder

package sqlout

import (
	"fmt"
	"strings"
	"time"

	"github.com/elastic/elastic-agent-libs/config"
)

type sqlConfig struct {
	Hosts         []string         `config:"hosts" validate:"required"`
	Username      string           `config:"username" validate:"required"`
	Password      string           `config:"password" validate:"required"`
	DBName        string           `config:"dbname" validate:"required"`
	TablenameRoot string           `config:"tablenameroot"`
	BulkMaxSize   int              `config:"bulk_max_size"`
	MaxRetries    int              `config:"max_retries"`
	LoadBalance   bool             `config:"loadbalance"`
	Backoff       Backoff          `config:"backoff"`
	Queue         config.Namespace `config:"queue"`
}

type Backoff struct {
	Init time.Duration
	Max  time.Duration
}

func defaultConfig(beatname string) sqlConfig {
	return sqlConfig{
		Hosts:         []string{"localhost:3306"},
		TablenameRoot: beatname,
		BulkMaxSize:   50,
		MaxRetries:    3,
		LoadBalance:   true,
		Backoff: Backoff{
			Init: 1 * time.Second,
			Max:  60 * time.Second,
		},
	}
}

func (c *sqlConfig) Validate() error {
	if strings.Contains(c.TablenameRoot, "`") {
		return fmt.Errorf("the tablenameroot may not include \"`\" and \".\". ")
	}

	return nil
}
