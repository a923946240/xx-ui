package externalproxy

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type logBuffer struct {
	mu            sync.Mutex
	text, pending string
	secrets       []string
}

func newLogBuffer(s Spec) *logBuffer {
	secrets := []string{base64.StdEncoding.EncodeToString([]byte(s.Username + ":" + s.Password))}
	for _, raw := range []string{s.Password, s.Username} {
		quoted, _ := json.Marshal(raw)
		toml := strconv.Quote(raw)
		secrets = append(secrets, raw, url.QueryEscape(raw), url.PathEscape(raw), string(quoted[1:len(quoted)-1]), toml[1:len(toml)-1])
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &logBuffer{secrets: secrets}
}

func (l *logBuffer) redact(s string) string {
	for _, secret := range l.secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

func (l *logBuffer) append(s string) {
	l.text += l.redact(s)
	if len(l.text) > 16384 {
		l.text = l.text[len(l.text)-16384:]
	}
}

func (l *logBuffer) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.append(s + "\n")
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending += string(p)
	for {
		i := strings.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		l.append(l.pending[:i+1])
		l.pending = l.pending[i+1:]
	}
	if len(l.pending) > 8192 {
		l.pending = "[oversized client log line omitted]"
	}
	return len(p), nil
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text
}
