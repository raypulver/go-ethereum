package ws

import (
	"encoding/json"

	"github.com/ethereum/go-ethereum/logger"
	"github.com/ethereum/go-ethereum/logger/glog"
	"github.com/ethereum/go-ethereum/xeth"
)

// WS methods
const (
	Quit                = "quit"
	MinerStart          = "miner_start"
	MinerStop           = "miner_stop"
	MinerHashrate       = "miner_hashrate"
	ImportPresaleWallet = "import_presale_wallet"
)

func init() {
	// register WS methods handlers
	actions[Quit] = quit
	actions[MinerStart] = minerStart
	actions[MinerStop] = minerStop
	actions[MinerHashrate] = minerHashrate
	actions[ImportPresaleWallet] = importPresaleWallet
}

// websocket API stateless handler type
type RequestHandler func(eth *xeth.XEth, req *WSRequest, res *interface{}) error

func quit(eth *xeth.XEth, req *WSRequest, res *interface{}) error {
	eth.StopBackend()
	return nil
}

func minerStart(eth *xeth.XEth, wsreq *WSRequest, wsres *interface{}) error {
	var req MinerStartRequest
	json.Unmarshal(wsreq.Params, &req)

	if eth.SetMining(true, req.NumThreads) {
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

func importPresaleWallet(eth *xeth.XEth, req *WSRequest, res *interface{}) error {
	var params ImportPresaleWalletRequest
	err := json.Unmarshal(req.Params, &params)
	if err != nil {
		return err
	}

	acc, err := eth.ImportPresaleWallet(params.Path, params.Password)
	if err == nil {
		res = &ImportPresaleWalletResponse{Address: acc.Address}
	}

	return err
}
