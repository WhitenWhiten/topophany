package management

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func DecodeTopology(raw []byte) (model.Config, error) {
	var c model.Config
	if e := Decode(raw, &c, 4<<20); e != nil {
		return c, e
	}
	config.Defaults(&c)
	return c, config.Validate(c)
}
