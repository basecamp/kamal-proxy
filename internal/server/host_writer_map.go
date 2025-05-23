package server

import (
	"net/url"
	"sync"
)

type HostWriterMap struct {
	hosts      map[string]string
	lock       sync.RWMutex
	pathLength int
}

func NewHostWriterMap(pathLength int) *HostWriterMap {
	m := &HostWriterMap{
		hosts:      make(map[string]string),
		pathLength: pathLength,
	}

	return m
}

func (m *HostWriterMap) Set(u *url.URL, writer string) {
	host := m.extractHost(u)

	m.lock.Lock()
	defer m.lock.Unlock()

	m.hosts[host] = writer
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
