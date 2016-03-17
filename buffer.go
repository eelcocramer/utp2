package utp

import "sync"

type buffer struct {
	pushChan   chan interface{}
	popChan    chan interface{}
	b          []interface{}
	size       int
	begin, end int
	m          sync.Mutex
	cond       *sync.Cond
	closed     bool
}

func NewBuffer(size int) *buffer {
	b := &buffer{
		pushChan: make(chan interface{}),
		popChan:  make(chan interface{}),
		b:        make([]interface{}, size),
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

func (b *buffer) Pop() interface{} {
	b.m.Lock()
	for b.size == 0 && !b.closed {
		b.cond.Wait()
	}
	defer b.m.Unlock()
	if b.size > 0 {
		i := b.b[b.begin]
		b.begin = (b.begin + 1) % len(b.b)
		b.size--
		return i
	} else {
		return nil
	}
}

func (b *buffer) Close() {
	close(b.pushChan)
}
