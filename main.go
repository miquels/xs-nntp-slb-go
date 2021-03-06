package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const banner string = "200 %s xs-nntp-slb-go ready (transit mode)\r\n";

type NNTPCmd struct {
	name      string
	minargs   int
	maxargs   int
	fun       func(sess *NNTPSession, line string, argv []string) (error)
	help      string
}
var nntpcmds []*NNTPCmd

type NNTPStats struct {
	accepted	uint64
	refused		uint64
	rejected	uint64
	tempfail	uint64
	takethis	uint64
	ihave		uint64
}
var nntpstats NNTPStats

var nntpclients []*NNTPSession
var nntpserver *NNTPSession
var ihave_sess *NNTPSession
var startTime time.Time = time.Now()

//
//	Fast jenkins hash
//
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

//
//	Slower MD5 hash (still pretty fast)
//	Used for compatibility with the C version.
//
func md5hash(key string) (res uint64) {
        hash := md5.Sum([]byte(key))
        res = uint64(hash[0]) + (uint64(hash[1]) << 8) +
                (uint64(hash[2]) << 16) + (uint64(hash[3]) << 24) +
                (uint64(hash[4]) << 32) + (uint64(hash[5]) << 40) +
                (uint64(hash[6]) << 48) + (uint64(hash[7]) << 56)
        return
}

func map_client(msgid string) *NNTPSession {
	return nntpclients[int(md5hash(msgid) % uint64(len(nntpclients)))]
}

func addPort(addr string, port string) (ret string) {
	ret = addr
	colons := strings.Count(addr, ":")
	if addr[0] == '[' || colons == 1 {
		return
	}
	if colons > 1 {
		ret = "[" + addr + "]:" + port
	} else {
		ret = addr + ":" + port
	}
	return
}

func updateStats(code int) {
	var ptr1, ptr2 *uint64
	switch code {
		// IHAVE
		case 235:
			ptr1 = &nntpstats.accepted
			ptr2 = &nntpstats.ihave
		case 435:
			ptr1 = &nntpstats.refused
			ptr2 = &nntpstats.ihave
		case 436:
			ptr1 = &nntpstats.tempfail
			ptr2 = &nntpstats.ihave
		case 437:
			ptr1 = &nntpstats.rejected
			ptr2 = &nntpstats.ihave
		// CHECK + TAKETHIS
		case 239:
			ptr1 = &nntpstats.accepted
			ptr2 = &nntpstats.takethis
		case 431:
			ptr1 = &nntpstats.tempfail
			ptr2 = &nntpstats.takethis
		case 438:
			ptr1 = &nntpstats.refused
			ptr2 = &nntpstats.takethis
		case 439:
			ptr1 = &nntpstats.rejected
			ptr2 = &nntpstats.takethis
		default:
	}
	if ptr1 != nil {
		atomic.AddUint64(ptr1, 1)
	}
	if ptr2 != nil {
		atomic.AddUint64(ptr2, 1)
	}
}

func logStats() {
	secs := int(time.Since(startTime).Seconds())
	n := &nntpstats
	Log.Notice("%s: stats: accepted=%d refused=%d rejected=%d " +
		"tempfail=%d takethis=%d ihave=%d seconds=%d",
		nntpserver.name,
		n.accepted, n.refused, n.rejected,
		n.tempfail, n.takethis, n.ihave, secs)
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
		err = fmt.Errorf("connect: %s", err.Error())
		return
	}
	sess = NewNNTPSession(conn, name)

	conn.SetDeadline(time.Now().Add(tmout))
	line, err := sess.ReadLine()
	if err != nil {
		return
	}
	if (len(line) == 0 || line[0] != '2') {
		err = fmt.Errorf("connect failed: %s", ChompString(line))
		return
	}

	conn.SetDeadline(time.Now().Add(tmout))
	xclient := nntpserver.conn.RemoteAddr().(*net.TCPAddr).IP.String()
	err = sess.WriteAndFlush(fmt.Sprintf("XCLIENT %s\r\n", xclient))
	if err != nil {
		err = fmt.Errorf("lost connection: %s", err)
		return
	}

	conn.SetDeadline(time.Now().Add(tmout))
	line, err = sess.ReadLine()
	if err != nil {
		return
	}
	if (len(line) == 0 || line[0] != '2') {
		err = fmt.Errorf("XCLIENT failed: %s", ChompString(line))
		return
	}

	conn.SetDeadline(time.Time{})
	return
}

//
//	Send a simple reply
//
func sendreply(sess *NNTPSession, cmd string, line string) (err error) {
	req := &NNTPReq{
		line : line,
		cmd : cmd,
		ready : true,
	}
	sess.q.Add(req, true)
	return
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
	if multi {
		err = c.Write(line)
		if err == nil {
			err = sess.CopyDotCRLF(c)
		}
	} else {
		err = c.WriteAndFlush(line)
	}
	return
}

func cmd_ihave(sess *NNTPSession, line string, arg []string) (err error) {
	if sess.q.Len() > 0 {
		sendreply(sess, arg[0],
			"436 This command MUST NOT be pipelined\r\n")
		return
	}
	c := map_client(arg[1])
	err = cmd_forward(sess, c, line, arg, false)
	if err == nil {
		ihave_sess = c
	}
	return
}

//
//	Send a simple command to a backend.
//
func cmd_simple(sess *NNTPSession, line string, arg []string) (err error) {
	c := map_client(arg[1])
	err = cmd_forward(sess, c, line, arg, false)
	return
}

//
//	Send a command + body to a backend
//
func cmd_withbody(sess *NNTPSession, line string, arg []string) (err error) {
	c := map_client(arg[1])
	err = cmd_forward(sess, c, line, arg, true)
	return
}

//
//	Quit command
//
func cmd_quit(sess *NNTPSession, line string, arg []string) (err error) {
	for _, c := range nntpclients {
		cmd_forward(sess, c, line, arg, false)
	}
	// note: QUIT in capitals means it won't get matched in nntpqueue.go
	if len(arg) == 1 {
		err = sendreply(sess, "QUIT", "205 Goodbye\r\n")
	}
	return
}

//
//	Help command
//
func cmd_help(sess *NNTPSession, line string, arg []string) (err error) {
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
	err = sendreply(sess, arg[0], r)
	return 
}

//
//	Capa command
//
func cmd_capa(sess *NNTPSession, line string, arg []string) (err error) {
	r := "101 Capability list:\r\n"
	r += "version 2\r\n"
	r += "implementation xs-nntp-slb-go\r\n"
	r += "ihave\r\n"
	r += "streaming\r\n"
	r += ".\r\n"
	err = sendreply(sess, arg[0], r)
	return
}

//
//	Mode command
//
func cmd_mode(sess *NNTPSession, line string, arg []string) (err error) {
	var r string
	what := strings.ToLower(arg[1])
	if (what != "stream") {
		r = "501 Unknown MODE variant\r\n"
	} else {
		r = "203 Streaming permitted\r\n"
	}
	err = sendreply(sess, arg[0], r)
	return
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
			logStats()
			Log.Fatal("%s: unexpected: %s (FATAL)", sess.name, err)
		}
		var code int64
		if len(line) > 2 {
			code, _ = strconv.ParseInt(line[0:3], 10, 16)
		}
		if code == 0 {
			Log.Error("%s: cannot parse reply code: %s",
				sess.name, line)
		}

		// So we have a reply. It corresponds to the oldest
		// command in our local queue. Pop it from the local
		// queue, and update it.
		r := sess.q.PopFirst()
		if r == nil {
			logStats()
			Log.Fatal("%s: got unexpected reply (command " +
				  "queue empty) (FATAL)", sess.name)
		}
		r.code = int(code)
		r.line = line

		if r.code > 0 {
			updateStats(r.code)
		}

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
		Log.Error("%s: unexpected: %s", sess.name, err.Error())
		return
	}

	for {
		line, err := sess.ReadLine()
		if err != nil {
			if err == io.EOF && sess.q.Len() == 0 {
				Log.Notice("%s: EOF", sess.name)
				// send QUIT to backends.
				cmd_quit(sess, "quit\r\n",
					[]string{"quit", "quiet"})
				break
			}
			logStats()
			Log.Fatal("%s: unexpected: %s (qlen=%d) (FATAL)",
				sess.name, err.Error(), sess.q.Len())
		}
		lastcode := sess.q.LastCode()
		if ihave_sess != nil && lastcode == 335 {
			//
			// Last code we saw was a 335 reply
			// to IHAVE - forward article now.
			//
			arg := []string{ "ihave" }
			err = cmd_forward(sess, ihave_sess, line, arg, true)
			if err != nil {
				logStats()
				Log.Fatal("%s: error during IHAVE forward" +
					  " to %s: %s (FATAL)", sess.name,
					  ihave_sess.name, err.Error())
			}
			ihave_sess = nil
			continue
		}
		ihave_sess = nil

		words := strings.Fields(line)
		if len(words) == 0 {
			// most NNTP servers seem to ignore empty lines
			continue
		}

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
				err = c.fun(sess, line, words)
				if err != nil {
					logStats()
					Log.Fatal("%s: error on %s: %s (FATAL)",
					sess.name, words[0], err.Error())
				}
			}
		}
		if (found == false) {
			Log.Error("%s: unknown command: %s", sess.name, line)
			sendreply(sess, cmd, "500 What?\r\n")
		}
		if cmd == "quit" {
			Log.Notice("%s: QUIT", sess.name)
			break
		}
	}
	logStats()
}

func main() {
	nntpcmds = def_nntpcmds

	var gomaxprocs int 
	var cpuprofile string
	var remote string
	var listen string

	Log.SetOutput(LogSyslog|LogStderr)

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
		Log.SetOutput(LogSyslog)
		conn, err = net.FileConn(os.Stdin)
	}
	if err != nil {
		Log.Fatal(err.Error())
	}

	rem := conn.RemoteAddr().(*net.TCPAddr).IP.String()
	names, err := net.LookupAddr(rem)
	if err == nil && len(names) > 0 && len(names[0]) > 1 {
		rem = names[0]
		l := len(rem) - 1
		if rem[l] == '.' {
			rem = rem[:l]
		}
	}
		
	nntpserver = NewNNTPSession(conn, rem)
	nntpserver.q.sess = nntpserver

	// connect to all remote servers
	if len(remote) == 0 {
		remote = os.Getenv("REALSERVERS")
	}
	if len(remote) == 0 {
		Log.Fatal("-backend and $REALSERVERS not set")
	}
	var num int
	for _, rem := range strings.Split(remote, ",") {
		num++
		rem = addPort(rem, "119")
		s, err := NewNNTPClient(num, rem)
		if err != nil {
			for _, c := range nntpclients {
				c.Close()
			}
			nntpserver.CloseMsg("500 backend " + rem + ": " +
						err.Error() + "\r\n")
			Log.Fatal("%s:%d: %s (FATAL)", rem, num, err.Error())
		}
		nntpclients = append(nntpclients, s)
	}

	doneChan := make(chan bool)
	for _, c := range nntpclients {
		go func (c *NNTPSession) {
			run_nntpclient(c)
			doneChan <- true
		}(c)
	}

	run_nntpserver(nntpserver)

	// Wait for all backends to QUIT
	Log.Info("%s: waiting for backends to shut down", nntpserver.name)

	var timeout bool
	timeChan := time.NewTimer(time.Second * 10).C

	for n := 0; n < len(nntpclients); n++ {
		select {
			case <- doneChan:
				// nothing, just loop
			case <- timeChan:
				timeout = true
				break
		}
	}

	if timeout {
		Log.Error("%s: timeout waiting for backend(s) to close",
				nntpserver.name)
	} else {
		nntpserver.q.Run()
		nntpserver.Close()
	}

	Log.Notice("%s: exit", nntpserver.name)
}

