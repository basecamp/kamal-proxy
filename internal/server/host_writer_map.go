package server

import (
	"encoding/gob"
	"log/slog"
	"net/url"
	"os"
	"sync"
)

type HostWriterMap struct {
	hosts       map[string]string
	lock        sync.RWMutex
	pathLength  int
	persistPath string
}

func NewHostWriterMap(pathLength int, persistPath string) *HostWriterMap {
	m := &HostWriterMap{
		hosts:       make(map[string]string),
		pathLength:  pathLength,
		persistPath: persistPath,
	}

	m.load()
	return m
}

func (m *HostWriterMap) Set(u *url.URL, writer string) {
	host := m.extractHost(u)

	m.lock.Lock()
	defer m.lock.Unlock()

	prior := m.hosts[host]
	if prior != writer {
		m.hosts[host] = writer
		m.save()
	}
}

func (m *HostWriterMap) Get(u *url.URL) string {
	host := m.extractHost(u)

	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.hosts[host]
}

// Private

func (m *HostWriterMap) extractHost(u *url.URL) string {
	return u.Host + m.firstSegments(u.Path, m.pathLength)
}

func (m *HostWriterMap) firstSegments(path string, count int) string {
	for i, ch := range path {
		if ch == '/' {
			if count <= 0 {
				return path[:i]
			}
			count--
		}
	}
	return path
}

func (m *HostWriterMap) load() {
	f, err := os.Open(m.persistPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("No saved host writer map", "path", m.persistPath, "error", err)
		} else {
			slog.Error("Unable to load host writer map", "path", m.persistPath, "error", err)
		}
		return
	}
	defer f.Close()

	err = gob.NewDecoder(f).Decode(&m.hosts)
	if err != nil {
		slog.Error("Unable to load host writer map", "path", m.persistPath, "error", err)
	}
}

func (m *HostWriterMap) save() {
	f, err := os.Create(m.persistPath)
	if err != nil {
		slog.Error("Unable to persist host writer map", "path", m.persistPath, "error", err)
	}
	defer f.Close()

	err = gob.NewEncoder(f).Encode(m.hosts)
	if err != nil {
		slog.Error("Unable to persist host writer map", "path", m.persistPath, "error", err)
	}
}
