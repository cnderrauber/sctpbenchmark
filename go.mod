module github.com/cnderrauber/sctpbenchmark

go 1.22.1

require (
	github.com/pion/logging v0.2.2
	github.com/pion/sctp v1.8.17-0.20240626134226-ab1574ae95d9
	github.com/urfave/cli/v2 v2.27.1
)

require github.com/gammazero/deque v0.2.1 // indirect

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/pion/datachannel v1.5.6
	github.com/pion/randutil v0.1.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/xrash/smetrics v0.0.0-20201216005158-039620a65673 // indirect
)

// replace github.com/pion/sctp => ../sctp
