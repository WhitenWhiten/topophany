package app

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/importer"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"io"
	"os"
)

func (a *App) importTopology(args []string) error {
	f := a.flags("import")
	format := f.String("format", "", "ssh, compose (rendered JSON), or tailscale (status JSON)")
	input := f.String("input", "", "local inventory file; no commands are executed")
	output := f.String("output", "", "new topology path; omit for candidate report")
	host := f.String("host", "", "reviewed deployment host for Compose published ports")
	pos, err := parseMixed(f, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 || *input == "" {
		return errors.New("use topo import --format FORMAT --input FILE [--output NEW_FILE]")
	}
	file, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("import input must be a regular local file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil {
		return err
	}
	result, err := importer.Build(*format, raw, *host)
	if err != nil {
		return err
	}
	if *output != "" {
		data, err := secure.MarshalJSON(result.Config, "  ")
		if err != nil {
			return err
		}
		if _, err = config.CommitFile(*output, config.FileVersion{}, append(data, '\n'), 0600); err != nil {
			return err
		}
	}
	return printJSON(a.Out, result)
}
