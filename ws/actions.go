package ws

import (
	"github.com/ethereum/go-ethereum/logger"
	"github.com/ethereum/go-ethereum/logger/glog"
	"github.com/ethereum/go-ethereum/xeth"
    "encoding/json"
)

// WS methods
const (
	Quit          = "quit"
	MinerStart    = "miner_start"
	MinerStop     = "miner_stop"
	MinerHashrate = "miner_hashrate"
)

func init() {
	// register WS methods handlers
	actions[Quit] = quit
	actions[MinerStart] = minerStart
	actions[MinerStop] = minerStop
	actions[MinerHashrate] = minerHashrate
}

// websocket API stateless handler type
type RequestHandler func(eth *xeth.XEth, req *WSRequest, res *interface{}) error

func quit(eth *xeth.XEth, req *WSRequest, res *interface{}) error {
	glog.V(logger.Error).Infoln("quit called :)")
	eth.StopBackend()
	return nil
}

func minerStart(eth *xeth.XEth, wsreq *WSRequest, wsres *interface{}) error {
    var req MinerStartRequest
    json.Unmarshal(wsreq.Params, &req)

    if !eth.SetMining(false, req.NumThreads) {
		return nil
	}
	return MinerNotStarted
}

func minerStop(eth *xeth.XEth, wsreq *WSRequest, wsres *interface{}) error {
    var req MinerStopRequest
    json.Unmarshal(wsreq.Params, &req)

	if !eth.SetMining(false, req.NumThreads) {
		return nil
	}
	return MinerNotStopped
}

func minerHashrate(eth *xeth.XEth, wsreq *WSRequest, wsres *interface{}) error {
	*wsres = &MinerHashrateResponse{Hashrate: eth.HashRate()}
	return nil
}
