package view

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"strings"
	"testing"
)

func TestLanguageDoesNotChangeEvidenceOrOtherViews(t *testing.T) {
	en := View{Snapshot: model.Snapshot{Topology: model.Config{Services: []model.Service{{ID: "web"}}}}}
	zh := en
	zh.Language = "zh"
	ja := en
	ja.Language = "ja"
	if en.Verdict().State != zh.Verdict().State || en.Verdict().Text == zh.Verdict().Text || ja.Verdict().Text == zh.Verdict().Text {
		t.Fatal("language changed semantics or leaked")
	}
	if en.Verdict().Text != "NO CHECKS" {
		t.Fatal("English view changed")
	}
	coverage := zh.Coverage()
	if coverage.Gaps[0].Code != "unmonitored" || !strings.Contains(coverage.Gaps[0].Reason, "尚未配置") || !strings.Contains(coverage.Line(), "服务可用性") {
		t.Fatal(coverage)
	}
}
