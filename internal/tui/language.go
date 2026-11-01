package tui

import "github.com/WhitenWhiten/topophany/internal/view"

func (m *Model) ui(text string) string { return view.Text(m.Language, text) }
