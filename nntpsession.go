package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

var debugFile = "/tmp/xs-nntp-slb-go.dbg"

type NNTPSession struct {
	conn net.Conn
	name string
	r    *bufio.Reader
	w    *bufio.Writer
	q    NNTPQueue
	dbgFile *os.File
	dbgName string
}

func NewNNTPSession(conn net.Conn, name string) *NNTPSession {
	sess := &NNTPSession{
		conn: conn,
		name: name,
		r : bufio.NewReaderSize(conn, 32768),
		w : bufio.NewWriterSize(conn, 32768),
	}
	return sess
}

func (sess *NNTPSession) enableDbg(fn string) {
	file, err := os.OpenFile(fn, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		s := fmt.Sprintf("started for %s pid %d\n", sess.name, os.Getpid())
		sess.dbgFile = file
		sess.dbgName = fn
		sess.dbgFile.WriteString(s)
	}
}

func (sess *NNTPSession) writeDbg(dir string, line string) {
	if sess.dbgFile == nil {
		return
	}
	sess.dbgFile.WriteString(dir + " " + line)
}

func (sess *NNTPSession) ReadLine() (line string, err error) {
	line, err = sess.r.ReadString('\n')
	sess.writeDbg("<<", line)
	return
}

func (sess *NNTPSession) Write(line string) (err error) {
	sess.writeDbg(">>", line)
	_, err = sess.w.WriteString(line)
	return
}

func (sess *NNTPSession) Flush() (err error) {
	err = sess.w.Flush()
	return
}

func (sess *NNTPSession) WriteAndFlush(line string) (err error) {
	sess.writeDbg(">>", line)
	if _, err = sess.w.WriteString(line); err == nil {
		err = sess.w.Flush()
	}
	return
}

//
//	Copy from sess to out, until we see \r\n.\r\n
//	FIXME: should probably do this with a Reader.
//
func (sess *NNTPSession) CopyDotCRLF(out *NNTPSession) (err error) {

	var line []byte
	var b byte

	state := 2
	for state != 5 {

		// state 0 is a special case. we use ReadSlice to
		// efficiently and without copying find the first CR
                if state == 0 {
                        line, err = sess.r.ReadSlice('\r')
                        if len(line) == 0 {
                                // error from ReadSlice
                                return
                        }
                        if err == nil {
                                state = 1
                        }
			_, err = out.w.Write(line)
			if err != nil {
				return
			}
                        continue
                }
                b, err = sess.r.ReadByte()
                if err != nil {
                        return
                }
		out.w.WriteByte(b)
		if err != nil {
			return
		}
                switch state {
                        case 0:
                                if (b == '\r') {
                                        state = 1
                                        continue
                                }
                        case 1:
                                if (b == '\n') {
                                        // start looking for dot
                                        state = 2
                                        continue
                                }
                                if (b == '\r') {
                                        // \r\r\r\r .. stay in state 1
                                        continue
                                }
                        case 2:
                                if b == '.' {
                                        state = 3
                                        continue
                                }
                                if b == '\r' {
                                        // \r\n\r .. state 1 again
                                        state = 1
                                        continue
                                }
                        case 3:
                                if (b == '\r') {
                                        state = 4
                                        continue
                                }
                        case 4:
                                if (b == '\n') {
                                        state = 5
                                        continue
                                }
                                if (b == '\r') {
                                        // \r\n.\r\r .. state 1 again
                                        state = 1
                                        continue
                                }
                }
                state = 0
        }
	err = out.w.Flush()
	return
}

func (sess *NNTPSession) Close() {
	Log.Info("%s: session closed", sess.name)
	sess.conn.Close()
	//if sess.dbgFile != nil {
	//	os.Remove(ses.dbgName)
	//}
}

func (sess *NNTPSession) CloseMsg(msg string) {
	Log.Info("%s: session closed", sess.name)
	sess.WriteAndFlush(msg)
	sess.conn.Close()
	//if sess.dbgFile != nil {
	//	os.Remove(ses.dbgName)
	//}
}

