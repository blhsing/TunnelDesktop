package tunnel

import (
	"io"
	"net"
	"sync"
)

type closeWriter interface {
	CloseWrite() error
}

type closeReader interface {
	CloseRead() error
}

func Pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go copyHalf(&wg, a, b)
	go copyHalf(&wg, b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

func copyHalf(wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	if cw, ok := dst.(closeWriter); ok {
		_ = cw.CloseWrite()
	} else {
		_ = dst.Close()
	}
	if cr, ok := src.(closeReader); ok {
		_ = cr.CloseRead()
	}
}
