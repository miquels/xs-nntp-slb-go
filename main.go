package main

import (
	"strings"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"
)

const banner string = "200 %s xs-nntp-slb-go ready (transit mode)\r\n";

type NNTPCmd struct {
	name      string
	minargs   int
	maxargs   int
	fun       func(sess *NNTPSession, line string, argv []string)
	help      string
}
var nntpcmds []*NNTPCmd

var nntpclients []*NNTPSession
var nntpserver *NNTPSession
var ihave_sess *NNTPSession

func jenkinshash(key string) (res uint32) {
    for _, e := range key {
        res += uint32(e)
        res += (res << 10);
        res ^= (res >> 6);
    }
    res += (res << 3);
    res ^= (res >> 11);
    res += (res << 15);
    return
}

func map_client(msgid string) *NNTPSession {
	return nntpclients[int(jenkinshash(msgid) % uint32(len(nntpclients)))]
}

//
// Connect to the backend server, wait for banner, send XCLIENT,
// and expect a 200 status code.
//
func NewNNTPClient(num int, rem string) (sess *NNTPSession, err error) {
	name := fmt.Sprintf("%s:%d", rem, num)
	tmout := time.Duration(10 * time.Second)

	Log.Info("%s: connecting", name)
	conn, err := net.DialTimeout("tcp", rem, tmout)
	if err != nil {
		Log.Error("%s: connect: %s", name, err)
		return
	}
	sess = NewNNTPSession(conn, name)

	conn.SetDeadline(time.Now().Add(tmout))
	line, err := sess.ReadLine()
	if err != nil {
		return
	}
	if (line[0] != '2') {
		err = fmt.Errorf("%s: connect failed: %s", sess.name, line)
		return
	}

	conn.SetDeadline(time.Now().Add(tmout))
	xclient := nntpserver.conn.RemoteAddr().(*net.TCPAddr).IP.String()
	err = sess.WriteAndFlush(fmt.Sprintf("XCLIENT %s\r\n", xclient))
	if err != nil {
		err = fmt.Errorf("%s: lost connection: %s", sess.name, err)
		return
	}

	conn.SetDeadline(time.Now().Add(tmout))
	line, err = sess.ReadLine()
	if err != nil {
		return
	}
	// XXX DEBUG FIXME
	//if (line[0] != 2) {
	//	err = fmt.Errorf("%s: XCLIENT: failed: %s", sess.name, line)
	//	return
	//}

	conn.SetDeadline(time.Time{})
	return
}

//
//	Send a simple reply
//
func sendreply(sess *NNTPSession, cmd string, line string) {
	req := &NNTPReq{
		line : line,
		cmd : cmd,
		ready : true,
	}
	sess.q.Add(req, true)
}

//
//	Send a simple command to a backend.
//
func cmd_forward(sess *NNTPSession, c *NNTPSession, line string, arg []string, multi bool) (err error) {

	req := &NNTPReq{
		line : line,
		cmd: arg[0],
	}
	if len(arg) > 1 && arg[1][0] == '<' {
		req.msgid = arg[1]
	}

	// need to limit the amount of outstanding requests....
	// but this is pretty yucky FIXME
	for c.q.Len() > 50 {
		t := time.Duration(time.Millisecond * 10)
		time.Sleep(t)
	}

	// Add request to the main queue
	sess.q.Add(req, false)

	// Add request to the backend-specific queue
	c.q.Add(req, false)

	// And write request to backend
	err = c.WriteAndFlush(line)
	if err == nil && multi {
		err = sess.CopyDotCRLF(c)
	}
	return
}

func cmd_ihave(sess *NNTPSession, line string, arg []string) {
	c := map_client(arg[1])
	_ = cmd_forward(sess, c, line, arg, false)
	ihave_sess = c
}

//
//	Send a simple command to a backend.
//
func cmd_simple(sess *NNTPSession, line string, arg []string) {
	c := map_client(arg[1])
	_ = cmd_forward(sess, c, line, arg, false)
}

//
//	Send a command + body to a backend
//
func cmd_withbody(sess *NNTPSession, line string, arg []string) {
	c := map_client(arg[1])
	_ = cmd_forward(sess, c, line, arg, false)
}

//
//	Quit command
//
func cmd_quit(sess *NNTPSession, line string, arg []string) {
	for _, c := range nntpclients {
		_ = cmd_forward(sess, c, line, arg, false)
	}
	// note: QUIT in capitals means it won't get matched in nntpqueue.go
	sendreply(sess, "QUIT", "205 Goodbye\r\n")
}

//
//	Help command
//
func cmd_help(sess *NNTPSession, line string, arg []string) {
	r := "100 Legal commands\r\n";
	for _, c := range nntpcmds {
		var spc string
		if (c.help == "") {
			spc = ""
		} else {
			spc = " "
		}
		r += fmt.Sprintf("  %s%s%s\r\n", c.name, spc, c.help)
	}
	r += ".\r\n"
	sendreply(sess, arg[0], r)
}

//
//	Capa command
//
func cmd_capa(sess *NNTPSession, line string, arg []string) {
	r := "101 Capability list:\r\n"
	r += "version 2\r\n"
	r += "implementation xs-nntp-slb-go\r\n"
	r += "ihave\r\n"
	r += "streaming\r\n"
	r += ".\r\n"
	sendreply(sess, arg[0], r)
}

//
//	Mode command
//
func cmd_mode(sess *NNTPSession, line string, arg []string) {
	var r string
	what := strings.ToLower(arg[1])
	if (what != "stream") {
		r = "501 Unknown MODE variant\r\n"
	} else {
		r = "203 Streaming permitted\r\n"
	}
	sendreply(sess, arg[0], r)
}

// Command dispatch table
var def_nntpcmds = []*NNTPCmd{
	&NNTPCmd{"help", 0, 0, cmd_help, ""},
	&NNTPCmd{"capabilities", 0, 1, cmd_capa, "[keyword]"},
	&NNTPCmd{"mode", 1, 1, cmd_mode, "stream"},
	&NNTPCmd{"quit", 0, 0, cmd_quit, ""},
	&NNTPCmd{"check", 1, 1, cmd_simple, "message-id"},
	&NNTPCmd{"ihave", 1, 1, cmd_ihave, "message-id"},
	&NNTPCmd{"stat", 1, 1, cmd_simple, "message-id"},
	&NNTPCmd{"takethis", 1, 1, cmd_withbody, "message-id"},
}

//
// NNTP Client: read responses from backend and queue them to be
// sent back to the remote client.
//
func run_nntpclient(sess *NNTPSession) {
	defer sess.Close()

	for {
		line, err := sess.ReadLine()
		if err != nil {
			Log.Error("%s: unexpected: %s", sess.name, err)
			break
		}
		code, _ := strconv.ParseInt(line[0:3], 10, 16)
		if code == 0 {
			Log.Error("%s: cannot parse reply code: %s",
				sess.name, line)
			break
		}

		// So we have a reply. It corresponds to the oldest
		// command in our local queue. Pop it from the local
		// queue, and update it.
		r := sess.q.PopFirst()
		Log.Debug("%s: popped %s", sess.name, r.line)
		r.code = int(code)
		r.line = line

		// set to ready in the global queue
		nntpserver.q.Ready(r)

		// might be a reply to the "quit" command,
		// in that case, we're done!
		if r.cmd == "quit" {
			break
		}
	}
}

func run_nntpserver(sess *NNTPSession) {

	Log.Notice("%s: connected", sess.name)

	hostname, _ := os.Hostname()
	err := sess.WriteAndFlush(fmt.Sprintf(banner, hostname))
	if err != nil {
		Log.Error("%s: unexpected: %s", sess.name, err)
		return
	}

	for {
		line, err := sess.ReadLine()
		if err != nil {
			Log.Error("%s: unexpected: %s", sess.name, err)
			break
		}
		if sess.q.lastcode == 335 {
			//
			// Last code we saw was a 335 reply
			// to IHAVE - forward article now.
			//
			err = sess.CopyDotCRLF(ihave_sess)
			if err != nil {
				Log.Fatal("%s: error during IHAVE forward to %s: %s", sess.name, ihave_sess.name, err)
			}
			ihave_sess = nil
		}

		Log.Debug("<< %s", line)
		words := strings.Fields(line)
		words[0] = strings.ToLower(words[0])
		cmd := words[0]
		nargs := len(words) - 1
		found := false
		for _, c := range nntpcmds {
			if (c.name != cmd) {
				continue
			}
			found = true
			if (nargs < c.minargs || nargs > c.maxargs) {
				Log.Error("%s: syntax error: %s", sess.name, line)
				sendreply(sess, cmd, "435 syntax error\r\n")
			} else {
				c.fun(sess, line, words)
			}
		}
		if (found == false) {
			Log.Error("%s: unknown command: %s", sess.name, line)
			Log.Debug(">> 500 What?")
			sendreply(sess, cmd, "500 What?\r\n")
		}
		if cmd == "quit" {
			break
		}
	}
	Log.Notice("%s: seen QUIT", sess.name)
}

func main() {
	nntpcmds = def_nntpcmds

	var gomaxprocs int 
	var cpuprofile string
	var remote string
	var listen string

	flag.IntVar(&gomaxprocs, "gomaxprocs", 1, "number of threads")
	flag.StringVar(&cpuprofile, "cpuprofile", "", "filename.prof")
	flag.StringVar(&remote, "backend", "", "ip:port[,ip:port...]")
	flag.StringVar(&listen, "listen", "", "ip:port")
	flag.Parse()

	runtime.GOMAXPROCS(gomaxprocs)

	if cpuprofile != "" {
		f, err := os.Create(cpuprofile)
		if err != nil {
			Log.Fatal(err.Error())
		}
		pprof.StartCPUProfile(f)
		defer f.Close()
		defer pprof.StopCPUProfile()
	}

	var conn net.Conn
	var err error
	if len(listen) > 0 {
		Log.SetOutput(LogStderr)
		a, err := net.ResolveTCPAddr("tcp", listen)
		if err != nil {
			Log.Fatal(err.Error())
		}
		l, err := net.ListenTCP("tcp", a)
		if err != nil {
			Log.Fatal(err.Error())
		}
		conn, err = l.Accept()
	} else {
		conn, err = net.FileConn(os.Stdin)
	}
	if err != nil {
		Log.Fatal(err.Error())
	}
	rem := conn.RemoteAddr().(*net.TCPAddr).IP.String()
	nntpserver = NewNNTPSession(conn, rem)
	nntpserver.q.sess = nntpserver

	var wg sync.WaitGroup

	// connect to all remote servers
	if len(remote) == 0 {
		remote = os.Getenv("REMOTE")
	}
	if len(remote) == 0 {
		Log.Fatal("REMOTE not set")
	}
	var num int
	for _, rem := range strings.Split(remote, ",") {
		num++
		s, err := NewNNTPClient(num, rem)
		if err != nil {
			for _, c := range nntpclients {
				c.Close()
			}
			nntpserver.CloseMsg("500 " + err.Error() + "\r\n")
			Log.Fatal(err.Error())
		}
		nntpclients = append(nntpclients, s)
	}

	for _, c := range nntpclients {
		wg.Add(1)
		go func (c *NNTPSession) {
			defer wg.Done()
			run_nntpclient(c)
		}(c)
	}

	run_nntpserver(nntpserver)

	// Wait for all backends to QUIT
	Log.Notice("%s: waiting for all backends to shut down", nntpserver.name)
	wg.Wait()

	nntpserver.q.Run()
	nntpserver.Close()
	Log.Notice("%s: exit", nntpserver.name)
}

