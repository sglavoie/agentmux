package transcript

import (
	"container/list"
	"sync"
	"time"
)

// A search only needs main sessions in the TUI. This cap holds a typical
// active corpus while bounding memory for much larger or unfiltered indexes.
const maxSearchCacheBytes int64 = 256 << 20

type cachedSearchCorpus struct {
	key         string
	path        string
	fileSize    int64
	fileModTime time.Time
	legacy      string
	sources     sourceSidecar
	sourceAware bool
	bytes       int64
}

func (e *cachedSearchCorpus) text(source string) (string, bool) {
	if e.sourceAware {
		return sidecarText(e.sources, source), true
	}
	return e.legacy, false
}

type searchCorpusCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	oldest     *list.List
	bytes      int64
	generation uint64
}

func (c *searchCorpusCache) getSource(key string, meta SessionMeta, source string) (string, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return "", false, false
	}
	element, ok := c.entries[key]
	if !ok {
		return "", false, false
	}
	e := element.Value.(*cachedSearchCorpus)
	if e.path != meta.Path || e.fileSize != meta.FileSize || !e.fileModTime.Equal(meta.FileModTime) {
		c.remove(element)
		return "", false, false
	}
	c.oldest.MoveToBack(element)
	text, aware := e.text(source)
	return text, aware, true
}

func (c *searchCorpusCache) version() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *searchCorpusCache) put(key string, meta SessionMeta, raw string, sources sourceSidecar, sourceAware bool, generation uint64) {
	e := &cachedSearchCorpus{key: key, path: meta.Path, fileSize: meta.FileSize, fileModTime: meta.FileModTime}
	if sourceAware {
		e.sources, e.sourceAware = sources, true
		e.bytes = int64(len(sources.Any) + len(sources.User) + len(sources.Harness))
	} else {
		e.legacy = raw
		e.bytes = int64(len(raw))
	}
	if e.bytes > maxSearchCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
		c.oldest = list.New()
	}
	if old, ok := c.entries[key]; ok {
		c.remove(old)
	}
	for c.bytes+e.bytes > maxSearchCacheBytes && c.oldest.Len() > 0 {
		c.remove(c.oldest.Front())
	}
	c.entries[key] = c.oldest.PushBack(e)
	c.bytes += e.bytes
}

func (c *searchCorpusCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	if element, ok := c.entries[key]; ok {
		c.remove(element)
	}
}

func (c *searchCorpusCache) remove(element *list.Element) {
	e := element.Value.(*cachedSearchCorpus)
	delete(c.entries, e.key)
	c.oldest.Remove(element)
	c.bytes -= e.bytes
}
