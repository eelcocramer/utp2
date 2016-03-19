package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/h2so5/utp2"
)

type te struct{}

func (t *te) Index() int {
	return 999
}

func main() {
	numcpu := runtime.NumCPU()
	runtime.GOMAXPROCS(numcpu)

	b := utp.NewBuffer(15)
	go func() {
		for {
			b.SetDeadline(time.Now().Add(500 * time.Millisecond))
			p, err := b.Pop()
			if err != nil {
				fmt.Println(err)
				return
			}
			fmt.Println(p)
			time.Sleep(time.Second * 2)
		}
	}()

	b.Push(1)
	time.Sleep(time.Second)
	b.Push(2)
	time.Sleep(time.Second)
	b.Push(3)
	time.Sleep(time.Second)
	b.Push(4)
	time.Sleep(time.Second)
	b.Push(&te{})
	time.Sleep(time.Second)
	b.Push(6)
	time.Sleep(time.Second)
	b.Push(7)
	time.Sleep(time.Second)
	b.Push(8)

	for {

		/*
			addr, err := utp.ResolveAddr("utp", "")
			listener, err := utp.Listen("utp", addr)
			fmt.Println(listener.RawConn.LocalAddr(), err)

			b := make([]byte, 500)
			listener.RawConn.Close()
			n, addrx, err := listener.RawConn.ReadFrom(b)
			fmt.Println(n, addrx, err, "*", runtime.NumGoroutine())
			listener.AcceptUTP()
		*/
	}
}
