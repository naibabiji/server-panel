package router

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/naibabiji/server-panel/i18n"
)

// dynamicKeyPrefixes lists t('<literal-prefix>' + expr) call sites where the
// key is built at runtime (e.g. t('files.busy_'+actionKey)). The static
// scanner below can only see the literal prefix, which is never itself a
// real key, so these are expected non-matches rather than missing keys.
var dynamicKeyPrefixes = map[string]bool{
	"files.busy_": true,
	"files.done_": true,
}

var (
	goTemplateKeyRe = regexp.MustCompile(`\{\{t \.Lang "([a-zA-Z0-9_.]+)"`)
	jsKeyRe         = regexp.MustCompile(`\bt\('([a-zA-Z0-9_.]+)'`)
)

// collectKeys scans every templates/*.html file and static/js/app.js for
// keys used via the {{t .Lang "..."}} template func and the client-side
// t('...') JS helper. Go-template keys resolve server-side and don't need
// to be in i18nKeys; JS keys are only ever populated client-side from
// window.SERVER_PANEL_I18N, so they must also appear in i18nKeys.
func collectKeys(t *testing.T) (goTemplateKeys, jsKeys map[string]bool) {
	t.Helper()
	goTemplateKeys = map[string]bool{}
	jsKeys = map[string]bool{}

	scan := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)
		for _, m := range goTemplateKeyRe.FindAllStringSubmatch(content, -1) {
			goTemplateKeys[m[1]] = true
		}
		for _, m := range jsKeyRe.FindAllStringSubmatch(content, -1) {
			if dynamicKeyPrefixes[m[1]] {
				continue
			}
			jsKeys[m[1]] = true
		}
	}

	templates, err := filepath.Glob("../templates/*.html")
	if err != nil || len(templates) == 0 {
		t.Fatalf("glob templates: %v (found %d)", err, len(templates))
	}
	for _, p := range templates {
		scan(p)
	}
	scan("../static/js/app.js")

	return goTemplateKeys, jsKeys
}

// TestI18nKeysResolveInBothLocales ensures every key referenced by
// {{t .Lang "..."}} or the client-side t('...') helper actually resolves in
// both locale JSON files. A key that's missing falls back to rendering the
// literal dotted key on screen instead of erroring, so this would otherwise
// go unnoticed until someone spots it in the UI.
func TestI18nKeysResolveInBothLocales(t *testing.T) {
	goTemplateKeys, jsKeys := collectKeys(t)

	all := map[string]bool{}
	for k := range goTemplateKeys {
		all[k] = true
	}
	for k := range jsKeys {
		all[k] = true
	}

	var missingZh, missingEn []string
	for k := range all {
		if !i18n.HasKey(i18n.DefaultLang, k) {
			missingZh = append(missingZh, k)
		}
		if !i18n.HasKey(i18n.English, k) {
			missingEn = append(missingEn, k)
		}
	}
	sort.Strings(missingZh)
	sort.Strings(missingEn)

	if len(missingZh) > 0 {
		t.Errorf("keys missing from zh-CN.json: %v", missingZh)
	}
	if len(missingEn) > 0 {
		t.Errorf("keys missing from en-US.json: %v", missingEn)
	}
}

// TestI18nKeysExposedToClient ensures every key used via the client-side
// t('...') JS helper is present in i18nKeys (router.go), which is what
// populates window.SERVER_PANEL_I18N.messages. A key missing from that list
// renders as the literal dotted key in the browser instead of erroring.
func TestI18nKeysExposedToClient(t *testing.T) {
	_, jsKeys := collectKeys(t)

	exposed := map[string]bool{}
	for _, k := range i18nKeys {
		exposed[k] = true
	}

	var missing []string
	for k := range jsKeys {
		if !exposed[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("keys used via client-side t('...') but missing from i18nKeys in router.go: %v", missing)
	}
}
