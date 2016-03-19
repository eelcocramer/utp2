package utp

import (
	"sync"
	"time"
)

type indexed interface {
	Index() int
}

type buffer struct {
	pushChan   chan interface{}
	popChan    chan interface{}
	b          []interface{}
	size       int
	begin, end int
	m          sync.Mutex
	cond       *sync.Cond
	closed     bool
	deadline   time.Time
	timeout    bool
	index      map[int]interface{}
}

func NewBuffer(size int) *buffer {
	b := &buffer{
		pushChan: make(chan interface{}),
		popChan:  make(chan interface{}),
		b:        make([]interface{}, size),
		index:    make(map[int]interface{}),
	}
	b.cond = sync.NewCond(&b.m)

	go func() {
		for {
			p := <-b.pushChan
			if p == nil {
				b.m.Lock()
				b.closed = true
				b.cond.Signal()
				b.m.Unlock()
				return
			}
			b.m.Lock()
			b.b[b.end] = p
			if i, ok := p.(indexed); ok {
				b.index[i.Index()] = p
			}
			b.end = (b.end + 1) % len(b.b)
			if b.size < len(b.b) {
				b.size++
			} else {
				b.begin = (b.begin + 1) % len(b.b)
			}
			b.cond.Signal()
			b.m.Unlock()
		}
	}()

	return b
}

func (b *buffer) Push(i interface{}) {
	b.pushChan <- i
}

func (b *buffer) Pop() (interface{}, error) {
	b.m.Lock()
	defer b.m.Unlock()
	b.timeout = false
	if !b.deadline.IsZero() {
		d := b.deadline.Sub(time.Now())
		if d > 0 {
			go func() {
				time.Sleep(d)
				b.m.Lock()
				defer b.m.Unlock()
				b.timeout = true
				b.cond.Signal()
			}()
		} else {
			return nil, errTimeout
		}
	}
	for b.size == 0 && !b.closed && !b.timeout {
		b.cond.Wait()
	}
	if b.timeout {
		return nil, errTimeout
	} else if b.size > 0 {
		p := b.b[b.begin]
		b.begin = (b.begin + 1) % len(b.b)
		b.size--
		if i, ok := p.(indexed); ok {
			delete(b.index, i.Index())
		}
		return p, nil
	} else {
		return nil, errClosing
	}
}

func (b *buffer) Get(index int) interface{} {
	b.m.Lock()
	defer b.m.Unlock()
	return b.index[index]
}

func (b *buffer) SetDeadline(d time.Time) error {
	b.m.Lock()
	defer b.m.Unlock()
	if b.closed {
		return errClosing
	}
	b.deadline = d
	return nil
}

func (b *buffer) Close() {
	close(b.pushChan)
}
