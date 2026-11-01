// Package stream accepts bounded request lines and emits panel frames through a
// callback. Sources are replaceable; the core writes no files and holds no lock.
package stream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/monitor"
)

const MaxRequestBytes = 256 << 10
const MaxFrameBytes = monitor.MaxPanelBytes

type Request struct {
	ProfileRevision string           `json:"profile_revision,omitempty"`
	RetryNonce      string           `json:"retry_nonce,omitempty"`
	Schema          int              `json:"schema"`
	RequestID       string           `json:"request_id"`
	Source          string           `json:"source"`
	Credentials     string           `json:"credentials"`
	Profile         string           `json:"profile"`
	Project         string           `json:"project"`
	Observer        string           `json:"observer"`
	Scenario        int              `json:"scenario"`
	Lang            string           `json:"lang"`
	Interval        string           `json:"interval"`
	Template        monitor.Template `json:"template"`
}
type Error = monitor.FrameError

func failure(code, message string) *Error { return &Error{Code: code, Message: message} }

type Line struct {
	Data    []byte
	TooLong bool
}

// ReadLines drains oversized lines without retaining them. A caller that needs
// to interrupt a blocked Reader must close it when its context ends.
func ReadLines(ctx context.Context, r io.Reader, lines chan<- Line) {
	defer close(lines)
	reader := bufio.NewReaderSize(r, 32<<10)
	for {
		var data []byte
		tooLong := false
		for {
			fragment, err := reader.ReadSlice('\n')
			fragment = bytes.TrimSuffix(fragment, []byte{'\n'})
			if !tooLong && len(data)+len(fragment) <= MaxRequestBytes {
				data = append(data, fragment...)
			} else {
				tooLong = true
				data = nil
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if len(data) > 0 || tooLong || err == nil {
				select {
				case lines <- Line{Data: data, TooLong: tooLong}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
			break
		}
	}
}
func Decode(line Line) (Request, *Error) {
	var r Request
	if line.TooLong || len(line.Data) > MaxRequestBytes {
		return r, failure("invalid_request", "request exceeds 256 KiB")
	}
	if !utf8.Valid(line.Data) {
		return r, failure("invalid_request", "request must be UTF-8")
	}
	trimmed := bytes.TrimSpace(line.Data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return r, failure("invalid_request", "request must be one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(line.Data))
	if err := dec.Decode(&r); err != nil {
		return r, failure("invalid_request", "request must be one JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return r, failure("invalid_request", "request must be one JSON object")
	}
	if r.Schema != 1 {
		return r, failure("unsupported_protocol", "schema must be 1")
	}
	if r.Source == "" {
		r.Source = "client"
	}
	if r.Interval == "" {
		r.Interval = "5s"
	}
	if r.Source == "preview" && r.Lang == "" {
		r.Lang = "en"
	}
	bad := func(message string) (Request, *Error) { return r, failure("invalid_request", message) }
	if len(r.RequestID) > 64 {
		return bad("request_id exceeds 64 bytes")
	}
	if r.Source != "client" && r.Source != "preview" && r.Source != "direct" {
		return bad("source must be client, preview or direct")
	}
	if r.Credentials != "" && !filepath.IsAbs(r.Credentials) {
		return bad("credentials must be an absolute path")
	}
	if r.Profile != "" && !config.ID(r.Profile) || r.Project != "" && !config.ID(r.Project) || r.Observer != "" && !config.ID(r.Observer) {
		return bad("invalid profile, project or observer")
	}
	d, err := time.ParseDuration(r.Interval)
	if err != nil || d < time.Second || d > time.Hour {
		return bad("interval must be 1s..1h")
	}
	if r.Scenario < 0 {
		return bad("scenario must be nonnegative")
	}
	if len(r.ProfileRevision) > 128 || len(r.RetryNonce) > 128 {
		return bad("direct revision/nonce exceeds budget")
	}
	if r.Source != "direct" && r.ProfileRevision != "" {
		return bad("profile_revision requires direct")
	}
	if r.Source != "direct" && r.Source != "client" && r.RetryNonce != "" {
		return bad("retry_nonce requires client or direct")
	}
	if r.Source == "direct" && (r.Profile == "" || r.Credentials != "" || r.Scenario != 0 || r.Lang != "") {
		return bad("direct requires profile and no client/preview credentials/scenario/lang")
	}
	if r.Source == "client" && (r.Scenario != 0 || r.Lang != "") {
		return bad("scenario and lang require preview")
	}
	if r.Source == "preview" {
		if r.Credentials != "" || r.Profile != "" {
			return bad("credentials and profile require client")
		}
		if !slices.Contains([]string{"en", "ja", "zh"}, r.Lang) {
			return bad("lang must be en, ja or zh")
		}
	}
	if err := r.Template.Validate(r.Source); err != nil {
		return bad(fmt.Sprint(err))
	}
	return r, nil
}
