package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"sync"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/logging"
	"github.com/pion/sctp"
	"github.com/urfave/cli/v2"
)

const reportInterval = 10 * time.Second

func main() {
	app := &cli.App{
		Name: "sctp benchmark",
		Commands: []*cli.Command{
			{
				Name:    "server",
				Aliases: []string{"s"},
				Usage:   "start a server to receive data",
				Action:  server,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "addr",
						Usage: "address to listen on",
						Value: ":9901",
					},
					&cli.DurationFlag{
						Name:  "duration",
						Usage: "duration of test, default 1m",
						Value: time.Minute,
					},
					&cli.BoolFlag{
						Name:  "tcp",
						Usage: "test tcp protocol, compare with sctp",
					},
				},
			},
			{
				Name:    "client",
				Aliases: []string{"c"},
				Usage:   "start a client to send data",
				Action:  client,
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "rate",
						Usage: "rate in MB/s of sending data",
					},
					&cli.IntFlag{
						Name:  "size",
						Usage: "size in Byte of message, default 1024 Byte",
						Value: 1024,
					},
					&cli.DurationFlag{
						Name:  "duration",
						Usage: "duration of test, default 1m",
						Value: time.Minute,
					},
					&cli.StringFlag{
						Name:  "addr",
						Usage: "address to connect to",
						Value: "localhost:9901",
					},
					&cli.BoolFlag{
						Name:  "loss",
						Usage: "lossable channel",
						Value: false,
					},
					&cli.BoolFlag{
						Name:  "tcp",
						Usage: "test tcp protocol, compare with sctp",
					},
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}

func server(c *cli.Context) error {
	addr := c.String("addr")
	duration := c.Duration("duration")

	go func() {
		log.Println(http.ListenAndServe(":6060", nil))
	}()
	var reader io.ReadCloser
	if c.Bool("tcp") {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		defer l.Close()
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		defer conn.Close()
		reader = conn
	} else {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return err
		}
		conn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			return err
		}
		fmt.Println("server listen on", addr)
		defer conn.Close()

		conn.SetReadBuffer(4 * 1024 * 1024)

		config := sctp.Config{
			NetConn:              &UDPConn{UDPConn: conn},
			LoggerFactory:        logging.NewDefaultLoggerFactory(),
			MaxReceiveBufferSize: 4 * 1024 * 1024,
		}
		a, err := sctp.Server(config)
		if err != nil {
			log.Panic(err)
		}
		defer a.Close()

		dc, err := datachannel.Accept(a, &datachannel.Config{
			LoggerFactory: logging.NewDefaultLoggerFactory(),
		})
		if err != nil {
			return err
		}
		defer dc.Close()

		fmt.Println("accepted a channel", dc.Label)
		dc.SetReadDeadline(time.Now().Add(duration + time.Second))

		reader = dc
	}

	buf := make([]byte, 65536)
	var totalBytes int64
	start := time.Now()
	lastReport := start
	for {
		n, err := reader.Read(buf)
		if err != nil {
			break
		}

		totalBytes += int64(n)
		if time.Since(lastReport) > reportInterval {
			elapsed := time.Since(start)
			lastReport = time.Now()
			rate := float64(totalBytes) / 1024 / 1024 / elapsed.Seconds()
			fmt.Printf("received %.2f MBytes in %.1fs, rate %.2f MB/s\n", float64(totalBytes)/1024/1024, elapsed.Seconds(), rate)
		}
	}

	elapsed := time.Since(start)
	rate := float64(totalBytes) / 1024 / 1024 / elapsed.Seconds()
	fmt.Printf("received %.2f MBytes in %.1fs, rate %.2f MB/s\n", float64(totalBytes)/1024/1024, elapsed.Seconds(), rate)
	time.Sleep(10 * time.Second)
	return nil
}

func client(c *cli.Context) error {
	addr := c.String("addr")
	reliable := !c.Bool("loss")
	rate := c.Int("rate")
	msgSize := c.Int("size")
	duration := c.Duration("duration")

	var dc *datachannel.DataChannel
	var writer io.Writer
	if c.Bool("tcp") {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		writer = conn
	} else {
		conn, err := net.Dial("udp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		fmt.Println("client connect to", addr)

		config := sctp.Config{
			NetConn:       conn,
			LoggerFactory: logging.NewDefaultLoggerFactory(),
		}

		a, err := sctp.Client(config)
		if err != nil {
			return err
		}
		fmt.Println("scpt client connected")

		defer a.Close()

		var dcConfig datachannel.Config
		if reliable {
			dcConfig = datachannel.Config{
				ChannelType:   datachannel.ChannelTypeReliable,
				Negotiated:    false,
				Label:         "reliable",
				LoggerFactory: logging.NewDefaultLoggerFactory(),
			}
		} else {
			dcConfig = datachannel.Config{
				ChannelType:   datachannel.ChannelTypePartialReliableRexmitUnordered,
				Negotiated:    false,
				Label:         "lossable",
				LoggerFactory: logging.NewDefaultLoggerFactory(),
			}
		}
		dc, err = datachannel.Dial(a, 1, &dcConfig)
		if err != nil {
			return err
		}
		defer dc.Close()
		writer = dc
	}

	interval := 1 * time.Millisecond
	bytesPerLoop := int(time.Duration(rate*1024*1024) / (time.Second / interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	data := make([]byte, msgSize)
	var totalBytes int64
	start := time.Now()
	lastReport := start
	for range ticker.C {
		if dc != nil && dc.BufferedAmount() > 20*1024*1024 {
			continue
		}
		toWrite := bytesPerLoop
		for toWrite > 0 {
			size := len(data)
			if toWrite < size {
				size = toWrite
			}
			n, err := writer.Write(data[:size])
			if err != nil {
				return err
			}
			totalBytes += int64(n)
			toWrite -= n
		}
		if time.Since(start) > duration {
			break
		}

		if time.Since(lastReport) > reportInterval {
			elapsed := time.Since(start)
			lastReport = time.Now()
			rate := float64(totalBytes) / 1024 / 1024 / elapsed.Seconds()
			fmt.Printf("sent %.2f Mbytes in %.1fs, rate %.2f MB/s\n", float64(totalBytes)/1024/1024, elapsed.Seconds(), rate)
		}
	}

	sentRate := float64(totalBytes) / 1024 / 1024 / time.Since(start).Seconds()
	fmt.Printf("sent %.2f Mbytes in %.1fs, rate(target/actual) (%d/%.2f) MB/s\n", float64(totalBytes)/1024/1024, time.Since(start).Seconds(), rate, sentRate)

	return nil
}

type UDPConn struct {
	*net.UDPConn
	lock  sync.RWMutex
	raddr net.Addr
}

func (c *UDPConn) Read(b []byte) (n int, err error) {
	var addr net.Addr
	n, addr, err = c.UDPConn.ReadFrom(b)
	if err == nil {
		c.lock.Lock()
		if c.raddr == nil {
			c.raddr = addr
		}
		c.lock.Unlock()
	}
	return
}

func (c *UDPConn) Write(b []byte) (n int, err error) {
	c.lock.RLock()
	addr := c.raddr
	c.lock.RUnlock()
	if addr == nil {
		return 0, errors.New("remote address not set")
	}
	return c.UDPConn.WriteTo(b, addr)
}
