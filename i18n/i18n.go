// Package i18n provides minimal server-side translation for the panel UI.
// Locale files live in locales/*.json and are embedded into the binary.
package i18n

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

const (
	DefaultLang = "zh-CN"
	English     = "en-US"
	CookieName  = "sp_lang"
)

// P holds named parameters for T/TE ("{{name}}" placeholders in a message).
type P map[string]string

//go:embed locales/*.json
var localesFS embed.FS

var messages = loadMessages()

func NormalizeLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "en", "en-us", "en_us":
		return English
	case "zh", "zh-cn", "zh_cn", "cn":
		return DefaultLang
	default:
		return DefaultLang
	}
}

// LangFromRequest resolves the active language: ?lang= query param first,
// then the sp_lang cookie, then the default.
func LangFromRequest(r *http.Request) string {
	if r == nil {
		return DefaultLang
	}
	if lang := strings.TrimSpace(r.URL.Query().Get("lang")); lang != "" {
		return NormalizeLang(lang)
	}
	if cookie, err := r.Cookie(CookieName); err == nil {
		return NormalizeLang(cookie.Value)
	}
	return DefaultLang
}

// MaybeSetLanguageCookie persists an explicit ?lang= choice so it survives
// subsequent requests without the query param.
func MaybeSetLanguageCookie(w http.ResponseWriter, r *http.Request) {
	if r == nil || w == nil {
		return
	}
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	if lang == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    NormalizeLang(lang),
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 365,
		HttpOnly: false,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https" || r.Header.Get("X-Forwarded-Ssl") == "on"
}

// HasKey reports whether key resolves to a real translation in lang,
// without DefaultLang fallback. Meant for build-time/test completeness
// checks, not for rendering (use T for that).
func HasKey(lang, key string) bool {
	return lookup(NormalizeLang(lang), key) != ""
}

// T looks up key in lang, falling back to DefaultLang and finally the raw
// key itself so a missing translation never renders blank.
func T(lang, key string, params ...P) string {
	value := lookup(NormalizeLang(lang), key)
	if value == "" && NormalizeLang(lang) != DefaultLang {
		value = lookup(DefaultLang, key)
	}
	if value == "" {
		return key
	}
	if len(params) > 0 {
		for name, replacement := range params[0] {
			value = strings.ReplaceAll(value, "{{"+name+"}}", replacement)
		}
	}
	return value
}

// TE is T with the language resolved from the request (query param/cookie).
func TE(r *http.Request, key string, params ...P) string {
	return T(LangFromRequest(r), key, params...)
}

func FuncMap() template.FuncMap {
	return template.FuncMap{
		"t": T,
	}
}

// ExposedMessages returns a flat key->text map for the given keys, meant to
// be embedded into a page as JSON for client-side JS lookups.
func ExposedMessages(lang string, keys []string) map[string]string {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = T(lang, key)
	}
	return result
}

func MessagesJSON(lang string, keys []string) template.JS {
	data, err := json.Marshal(ExposedMessages(lang, keys))
	if err != nil {
		return "{}"
	}
	return template.JS(data)
}

func loadMessages() map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, lang := range []string{DefaultLang, English} {
		data, err := localesFS.ReadFile("locales/" + lang + ".json")
		if err != nil {
			panic(err)
		}
		var locale map[string]any
		if err := json.Unmarshal(data, &locale); err != nil {
			panic(err)
		}
		result[lang] = locale
	}
	return result
}

func lookup(lang, key string) string {
	current, ok := messages[NormalizeLang(lang)]
	if !ok {
		current = messages[DefaultLang]
	}
	var value any = current
	for _, part := range strings.Split(key, ".") {
		nested, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = nested[part]
		if value == nil {
			return ""
		}
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}
