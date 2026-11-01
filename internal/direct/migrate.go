package direct

import (
	"errors"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// CollectorConfig preserves node/check identity and explicit upstream selection.
// It neither reads credential records nor starts collection. Paused checks make
// runtime credential provisioning and deployment an explicit next step.
func (p Profile) CollectorConfig(observer string) (model.Config, error) {
	if !config.ID(observer) {
		return model.Config{}, errors.New("invalid migration observer ID")
	}
	c, err := p.Config()
	if err != nil {
		return c, err
	}
	c.Observers = []model.Observer{{ID: observer, Name: "Source worker", Context: "collector-backed external resource executor", StaleSeconds: 90}}
	for i := range c.Sources {
		src := &c.Sources[i]
		src.Observer = observer
		src.AllowInsecure = false // direct-only consent cannot enter topology JSON
		if src.CredentialEnv != "" {
			src.CredentialEnv = "TOPO_SOURCE_" + strings.ToUpper(secure.Hash(src.ID)[:16])
		}
	}
	for i := range c.Checks {
		c.Checks[i].Observer = observer
		c.Checks[i].Disabled = true
	}
	if err := config.Validate(c); err != nil {
		return model.Config{}, errors.New("collector migration requires valid topology sources; non-loopback HTTP consent is direct-only, so configure HTTPS before exporting")
	}
	return c, nil
}
