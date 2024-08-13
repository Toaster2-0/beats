// read the README.md of the root folder

package sqlout

import (
	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/outputs"
	c "github.com/elastic/elastic-agent-libs/config"
)

func init() {
	outputs.RegisterType("sql", makeSqlout)
}

// makeSqlout instantiates a new sql output instance.
func makeSqlout(
	_ outputs.IndexManager,
	beat beat.Info,
	observer outputs.Observer,
	cfg *c.C,
) (outputs.Group, error) {
	config := defaultConfig(beat.Beat)
	if err := cfg.Unpack(&config); err != nil {
		return outputs.Fail(err)
	}

	clients := make([]outputs.NetworkClient, len(config.Hosts))
	for i, host := range config.Hosts {

		var client outputs.NetworkClient
		client = NewClient(config, host, observer)

		client = outputs.WithBackoff(client, config.Backoff.Init, config.Backoff.Max)
		clients[i] = client
	}

	return outputs.SuccessNet(config.Queue, config.LoadBalance, config.BulkMaxSize, config.MaxRetries, nil, clients)
}
