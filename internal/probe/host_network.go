package probe

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

const maxHostInterfaces = 4096

type hostInterfaceCounters struct{ rx, tx uint64 }

type hostNetworkSample struct {
	at         time.Time
	interfaces map[string]hostInterfaceCounters
}

// Use per-interface deltas so a reset cannot be masked by activity on another
// interface. Both samples must cover the same interfaces.
func hostNetworkRates(previous, current hostNetworkSample) model.NetworkReport {
	interval := current.at.Sub(previous.at).Seconds()
	if previous.at.IsZero() || interval <= 0 || len(previous.interfaces) == 0 || len(previous.interfaces) != len(current.interfaces) {
		return model.NetworkReport{}
	}
	var rx, tx float64
	for id, next := range current.interfaces {
		prev, ok := previous.interfaces[id]
		if !ok || next.rx < prev.rx || next.tx < prev.tx {
			return model.NetworkReport{}
		}
		rx += float64(next.rx - prev.rx)
		tx += float64(next.tx - prev.tx)
	}
	rx /= interval
	tx /= interval
	return model.NetworkReport{RXBytesPerSecond: &rx, TXBytesPerSecond: &tx}
}
