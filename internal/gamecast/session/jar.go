package session

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"PartyLAN/pkg/config"
)

const cookieFileName = "gamecast-cookies.json"

type StoredCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type cookieFile struct {
	Servers map[string][]StoredCookie `json:"servers"`
}

type Jar struct {
	mu   sync.Mutex
	path string
	jar  *cookiejar.Jar
}

func DefaultPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cookieFileName), nil
}

func New(path string) (*Jar, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Jar{
		path: path,
		jar:  inner,
	}, nil
}

func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.jar.Cookies(u)
}

func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jar.SetCookies(u, cookies)
}

func (j *Jar) Load(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	key := serverKey(u)

	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file cookieFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	stored := file.Servers[key]
	cookies := make([]*http.Cookie, 0, len(stored))
	for _, entry := range stored {
		if entry.Name == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:  entry.Name,
			Value: entry.Value,
			Path:  "/",
		})
	}
	j.jar.SetCookies(u, cookies)
	return nil
}

func (j *Jar) Save(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	key := serverKey(u)

	j.mu.Lock()
	defer j.mu.Unlock()

	file := cookieFile{Servers: map[string][]StoredCookie{}}
	data, err := os.ReadFile(j.path)
	if err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &file)
	}
	if file.Servers == nil {
		file.Servers = map[string][]StoredCookie{}
	}

	cookies := j.jar.Cookies(u)
	stored := make([]StoredCookie, 0, len(cookies))
	for _, cookie := range cookies {
		stored = append(stored, StoredCookie{
			Name:  cookie.Name,
			Value: cookie.Value,
		})
	}
	file.Servers[key] = stored

	if err := os.MkdirAll(filepath.Dir(j.path), 0755); err != nil {
		return err
	}
	data, err = json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(j.path, data, 0600)
}

func (j *Jar) Clear(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	key := serverKey(u)

	j.mu.Lock()
	defer j.mu.Unlock()

	file := cookieFile{Servers: map[string][]StoredCookie{}}
	data, err := os.ReadFile(j.path)
	if err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &file)
	}
	if file.Servers == nil {
		file.Servers = map[string][]StoredCookie{}
	}
	delete(file.Servers, key)
	current := j.jar.Cookies(u)
	expired := make([]*http.Cookie, 0, len(current))
	for _, cookie := range current {
		expired = append(expired, &http.Cookie{
			Name:   cookie.Name,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
	}
	j.jar.SetCookies(u, expired)

	if err := os.MkdirAll(filepath.Dir(j.path), 0755); err != nil {
		return err
	}
	data, err = json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(j.path, data, 0600)
}

func serverKey(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}
