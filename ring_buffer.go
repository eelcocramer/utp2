package utp

type ringBuffer struct {
	b     [][]byte
	begin int
	seq   uint16
}

func NewRingBuffer(n, size int, seq uint16) *ringBuffer {
	buf := make([][]byte, n)
	for i := 0; i < len(buf); i++ {
		buf[i] = make([]byte, size)
	}
	r := &ringBuffer{
		b:     buf,
		begin: 0,
		seq:   seq,
	}
	return r
}
