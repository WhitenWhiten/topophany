package preview

import (
	"bytes"
	"maps"
	"slices"
	"testing"
	"time"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/model"
)

var testNow = time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)

// Every catalog gives text to every reference in the topology and every
// scenario, and to nothing else.
func TestEveryLanguageHasEveryText(t *testing.T) {
	langs := Languages()
	if !slices.Contains(langs, DefaultLanguage) || len(langs) < 2 {
		t.Fatal("languages", langs)
	}
	for _, lang := range langs {
		raw, err := RawTopology(lang)
		if err != nil {
			t.Fatal(lang, err)
		}
		want := map[string]bool{}
		for _, m := range reference.FindAllSubmatch(raw, -1) {
			want[string(m[1])] = true
		}
		for _, sc := range scenarios {
			want["scenario."+sc.ID], want["scenario."+sc.ID+".detail"] = true, true
			if len(sc.DeclaresPops) > 0 {
				want["scenario."+sc.ID+".pops"] = true
			}
		}
		text, e := catalog(lang)
		if e != nil {
			t.Fatal(lang, e)
		}
		for key := range want {
			if text[key] == "" {
				t.Errorf("%s: no text for %s", lang, key)
			}
		}
		for key := range text {
			if !want[key] {
				t.Errorf("%s: %s names nothing", lang, key)
			}
		}
		b, e := PresetJSON(lang)
		if e != nil || reference.Match(b) || bytes.Contains(b, []byte(`"@`)) {
			t.Fatal(lang, "left a reference unresolved", e)
		}
		if _, e := Preset(lang); e != nil {
			t.Fatal(lang, e)
		}
	}
	en, _ := catalog(DefaultLanguage)
	for _, key := range slices.Sorted(maps.Keys(en)) {
		for _, r := range en[key] {
			if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
				t.Errorf("%s: %s is not written in English: %q", DefaultLanguage, key, en[key])
				break
			}
		}
	}
	if _, e := Preset("tlh"); e == nil {
		t.Fatal("an unsupported language was accepted")
	}
}

func TestLanguageFollowsTagsAndLocales(t *testing.T) {
	for tag, want := range map[string]string{
		"en": "en", "en_US.UTF-8": "en", "ja": "ja", "ja_JP.UTF-8": "ja", "JA-jp": "ja",
		"zh": "zh", "zh-CN": "zh", "zh_CN.GB18030": "zh", "zh-Hans-CN": "zh", "zh_TW@stroke": "zh",
		"": "", "C": "", "C.UTF-8": "", "POSIX": "", "fr_FR.UTF-8": "", "jav": "",
	} {
		if got := Language(tag); got != want {
			t.Errorf("Language(%q) = %q, want %q", tag, got, want)
		}
	}
}

// A language changes the words, never the evidence: every scenario judges
// every check and opens every incident alike in each language.
func TestLanguagesChangeOnlyWords(t *testing.T) {
	c, e := Preset(DefaultLanguage)
	if e != nil {
		t.Fatal(e)
	}
	ids := New(c, testNow, DefaultLanguage).ScenarioIDs()
	// Only the certificate scenario needs what the built-in network does
	// not declare: a check with a certificate warning window.
	builtIn := 0
	for _, sc := range scenarios {
		if sc.CertificateDays == 0 {
			builtIn++
		}
	}
	if len(ids) != builtIn {
		t.Fatal("the built-in topology hides scenarios", ids)
	}
	for _, id := range ids {
		if id == "collector-unreachable" || id == "cross-border-loss" || id == "chaos" {
			continue
		}
		base := settledIn(t, id, DefaultLanguage)
		for _, lang := range Languages() {
			snap := settledIn(t, id, lang)
			if len(snap.Checks) != len(base.Checks) || len(snap.Incidents) != len(base.Incidents) {
				t.Fatalf("%s in %s: %d checks, %d incidents; %d, %d in %s", id, lang, len(snap.Checks), len(snap.Incidents), len(base.Checks), len(base.Incidents), DefaultLanguage)
			}
			for i, c := range snap.Checks {
				b := base.Checks[i]
				if c.ID != b.ID || c.Health != b.Health || c.Freshness != b.Freshness || outcome(c) != outcome(b) {
					t.Fatalf("%s in %s: %s is %s/%s/%s, but %s/%s/%s in %s", id, lang, c.ID, c.Health, c.Freshness, outcome(c), b.Health, b.Freshness, outcome(b), DefaultLanguage)
				}
			}
			for i, inc := range snap.Incidents {
				if !slices.Equal(inc.Checks, base.Incidents[i].Checks) || inc.Hypothesis != base.Incidents[i].Hypothesis {
					t.Fatalf("%s in %s: incident %v differs from %v", id, lang, inc.Checks, base.Incidents[i].Checks)
				}
			}
		}
	}
}

func outcome(c model.CheckState) string {
	if c.Last == nil {
		return ""
	}
	return c.Last.Outcome
}

// Scenarios are named in the simulator's language.
func TestScenariosAreNamedInTheirLanguage(t *testing.T) {
	c, _ := Preset(DefaultLanguage)
	seen := map[string]string{}
	for _, lang := range Languages() {
		text, _ := catalog(lang)
		names := New(c, testNow, lang).Scenarios()
		if names[1] != text["scenario.transit-blocked"]+"\t"+text["scenario.transit-blocked.detail"] {
			t.Fatal(lang, names[1])
		}
		if other, ok := seen[names[1]]; ok {
			t.Fatalf("%s names its scenarios as %s does", lang, other)
		}
		seen[names[1]] = lang
	}
	if New(c, testNow, "tlh").Scenarios()[0] != New(c, testNow, DefaultLanguage).Scenarios()[0] {
		t.Fatal("an unsupported language did not fall back to the default")
	}
}
