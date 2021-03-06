package network

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iost-official/Go-IOS-Protocol/common"
	"github.com/iost-official/Go-IOS-Protocol/core/message"
	"github.com/iost-official/Go-IOS-Protocol/db"
	"github.com/iost-official/Go-IOS-Protocol/log"
	"github.com/iost-official/Go-IOS-Protocol/network/discover"
)

// const
const (
	HEADLENGTH              = 4
	CheckKnownNodeInterval  = 10
	NodeLiveCycle           = 2
	MaxDownloadRetry        = 2
	MsgLiveThresholdSeconds = 120
	RegisterServerPort      = 30304
	PublicMode              = "public"
	CommitteeMode           = "committee"
	RndBcastThreshold       = 0.5
)

// NetMode is the bootnode's mode.
var NetMode string

// Network defines network's API.
type Network interface {
	Broadcast(req message.Message)
	Send(req message.Message)
	Listen(port uint16) (<-chan message.Message, error)
	Close(port uint16) error
	Download(start, end uint64) error
	CancelDownload(start, end uint64) error
	QueryBlockHash(start, end uint64) error
	AskABlock(height uint64, to string) error
}

// NetConfig defines p2p net config.
type NetConfig struct {
	LogPath       string
	NodeTablePath string
	NodeID        string
	ListenAddr    string
	RegisterAddr  string
}

// BaseNetwork maintains all node table, and distributes the node table to all node.
type BaseNetwork struct {
	nodeTable     *db.LDBDatabase //all known node except remoteAddr
	neighbours    *sync.Map
	lock          sync.Mutex
	peers         peerSet // manage all connection
	RecvCh        chan message.Message
	listener      net.Listener
	RecentSent    *sync.Map
	NodeHeightMap map[string]uint64 //maintain all height of nodes higher than current height
	localNode     *discover.Node

	DownloadHeights *sync.Map //map[height]retry_times
	regAddr         string
	log             *log.Logger

	NodeAddedTime *sync.Map
}

// NewBaseNetwork returns a new BaseNetword instance.
func NewBaseNetwork(conf *NetConfig) (*BaseNetwork, error) {
	recv := make(chan message.Message, 100)
	var err error
	if conf.LogPath == "" {
		conf.LogPath, err = ioutil.TempDir(os.TempDir(), "iost_log_")
		if err != nil {
			return nil, fmt.Errorf("iost_log_path err: %v", err)
		}
	}
	if conf.NodeTablePath == "" {
		conf.NodeTablePath = "iost_node_table_"
	}
	srvLog, err := log.NewLogger(conf.LogPath)
	if err != nil {
		return nil, fmt.Errorf("failed to init log %v", err)
	}
	nodeTable, err := db.NewLDBDatabase(conf.NodeTablePath, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to init db %v", err)
	}
	NodeHeightMap := make(map[string]uint64, 0)
	if conf.NodeID == "" {
		conf.NodeID = string(discover.GenNodeID())
	}
	localNode := &discover.Node{ID: discover.NodeID(conf.NodeID), IP: net.ParseIP(conf.ListenAddr)}
	s := &BaseNetwork{
		nodeTable:       nodeTable,
		RecvCh:          recv,
		localNode:       localNode,
		neighbours:      new(sync.Map),
		log:             srvLog,
		NodeHeightMap:   NodeHeightMap,
		DownloadHeights: new(sync.Map),
		regAddr:         conf.RegisterAddr,
		RecentSent:      new(sync.Map),
		NodeAddedTime:   new(sync.Map),
	}
	return s, nil
}

// Listen listens local port, find neighbours.
func (bn *BaseNetwork) Listen(port uint16) (<-chan message.Message, error) {
	bn.localNode.TCP = port
	bn.log.D("[net] listening %v", bn.localNode)
	var err error
	bn.listener, err = net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(int(bn.localNode.TCP)))
	if err != nil {
		return bn.RecvCh, errors.New("failed to listen addr, err  = " + fmt.Sprintf("%v", err))
	}
	testListen, err := net.Dial("tcp4", bn.localNode.Addr())
	if err != nil {
		bn.log.E("Net type error!")
		os.Exit(1)
	}
	testListen.Close()
	go func() {
		for {
			conn, err := bn.listener.Accept()
			if err != nil {
				bn.log.E("[net] accept downStream node err:%v", err)
				time.Sleep(2 * time.Second)
				continue
			}
			go bn.receiveLoop(conn)
		}
	}()
	//register
	if bn.localNode.TCP == RegisterServerPort {
		go bn.nodeCheckLoop()
	} else {
		go bn.registerLoop()
		go bn.recentSentLoop()
	}
	return bn.RecvCh, nil
}

// Broadcast broadcasts msg to all node in the node table.
func (bn *BaseNetwork) Broadcast(msg message.Message) {
	if msg.From == "" {
		msg.From = bn.localNode.Addr()
	}
	from := msg.From

	bn.neighbours.Range(func(k, v interface{}) bool {
		node := v.(*discover.Node)
		if node.Addr() == from {
			return false
		}
		msg.To = node.Addr()
		bn.log.D("[net] broad msg: type= %v, from=%v,to=%v,time=%v, to node: %v", msg.ReqType, msg.From, msg.To, msg.Time, node.Addr())
		if !bn.isRecentSent(msg) {
			bn.broadcast(msg)
			prometheusSendBlockTx(msg)
		}
		return true
	})
}

func (bn *BaseNetwork) randomBroadcast(msg message.Message) {
	if msg.From == "" {
		msg.From = bn.localNode.Addr()
	}
	from := msg.From

	targetAddrs := make([]string, 0)
	bn.neighbours.Range(func(k, v interface{}) bool {
		node := v.(*discover.Node)
		if node.Addr() == from {
			return false
		}
		targetAddrs = append(targetAddrs, node.Addr())
		return true
	})
	if len(targetAddrs) == 0 {
		return
	}
	rand.Seed(time.Now().UnixNano())
	randomSlice := rand.Perm(len(targetAddrs))
	for i := 0; i < len(randomSlice)/2; i++ {
		msg.To = targetAddrs[randomSlice[i]]
		if !bn.isRecentSent(msg) {
			bn.log.D("[net] broad msg: type= %v, from=%v,to=%v,time=%v", msg.ReqType, msg.From, msg.To, msg.Time)
			bn.broadcast(msg)
			prometheusSendBlockTx(msg)
		}
	}
}

// broadcast broadcasts to all neighbours, stop broadcast when msg already broadcast
func (bn *BaseNetwork) broadcast(msg message.Message) {
	if msg.To == "" {
		return
	}
	node, _ := discover.ParseNode(msg.To)
	if msg.TTL == 0 || bn.localNode.Addr() == node.Addr() {
		return
	}
	msg.TTL = msg.TTL - 1
	data, err := msg.Marshal(nil)
	if err != nil {
		bn.log.E("[net] marshal request encountered err:%v", err)
	}
	req := newRequest(BroadcastMessage, bn.localNode.Addr(), data)
	peer, err := bn.dial(msg.To)
	if err != nil {
		bn.log.E("[net] broadcast dial tcp got err:%v", err)
		bn.nodeTable.Delete([]byte(msg.To))
		bn.NodeAddedTime.Delete(msg.To)
		return
	}
	if msg.ReqType == int32(ReqSyncBlock) || msg.ReqType == int32(ReqNewBlock) {
		if er := bn.send(peer.blockConn, req); er != nil {
			bn.log.E("[net] block conn sent error:%v", err)
			bn.peers.RemoveByNodeStr(msg.To)
		}
	} else {
		if er := bn.send(peer.conn, req); er != nil {
			bn.log.E("[net] normal conn sent error:%v", err)
			bn.peers.RemoveByNodeStr(msg.To)
		}
	}
}

func (bn *BaseNetwork) dial(nodeStr string) (*Peer, error) {
	bn.lock.Lock()
	defer bn.lock.Unlock()
	node, _ := discover.ParseNode(nodeStr)
	if bn.localNode.Addr() == node.Addr() {
		return nil, fmt.Errorf("dial local %v", node.Addr())
	}
	peer := bn.peers.Get(node)
	if peer == nil {
		bn.log.D("[net] dial to %v", node.Addr())
		conn, blockConn, err := dial(node.Addr())
		if err != nil {
			bn.log.E("failed to dial %v", err)
			return nil, err
		}
		go bn.receiveLoop(conn)
		go bn.receiveLoop(blockConn)
		peer := newPeer(conn, blockConn, bn.localNode.Addr(), nodeStr)
		bn.peers.Set(node, peer)
	}

	return bn.peers.Get(node), nil
}

func dial(nodeAddr string) (net.Conn, net.Conn, error) {
	conn, err := net.Dial("tcp4", nodeAddr)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		log.Report(&log.MsgNode{SubType: log.Subtypes["MsgNode"][2], Log: nodeAddr})
		return nil, nil, fmt.Errorf("dial tcp %v got err:%v", nodeAddr, err)
	}
	blockConn, err := net.Dial("tcp4", nodeAddr)
	if err != nil {
		if blockConn != nil {
			blockConn.Close()
		}
		log.Report(&log.MsgNode{SubType: log.Subtypes["MsgNode"][2], Log: nodeAddr})
		return nil, nil, fmt.Errorf("dial tcp %v got err:%v", nodeAddr, err)
	}
	return conn, blockConn, nil
}

// Send sends msg to msg.To.
func (bn *BaseNetwork) Send(msg message.Message) {
	if msg.To == bn.localNode.Addr() || msg.To == "" {
		return
	}
	data, err := msg.Marshal(nil)
	if err != nil {
		bn.log.E("[net] marshal request encountered err:%v", err)
	}
	bn.log.D("[net] send msg: type= %v, from=%v,to=%v,time=%v", msg.ReqType, msg.From, msg.To, msg.Time)
	req := newRequest(Message, bn.localNode.Addr(), data)
	peer, err := bn.dial(msg.To)
	if err != nil {
		bn.nodeTable.Delete([]byte(msg.To))
		bn.NodeAddedTime.Delete(msg.To)
		bn.log.E("[net] Send, dial tcp got err:%v", err)
		return
	}

	if msg.ReqType == int32(ReqSyncBlock) || msg.ReqType == int32(ReqNewBlock) {
		if er := bn.send(peer.blockConn, req); er != nil {
			bn.peers.RemoveByNodeStr(msg.To)
		}
	} else {
		if er := bn.send(peer.conn, req); er != nil {
			bn.peers.RemoveByNodeStr(msg.To)
		}
	}

	prometheusSendBlockTx(msg)
}

// Close closes all connection.
func (bn *BaseNetwork) Close(port uint16) error {
	if bn.listener != nil {
		bn.listener.Close()
	}
	return nil
}

func (bn *BaseNetwork) send(conn net.Conn, r *Request) error {
	if conn == nil {
		bn.log.E("[net] from %v,send data = %v, conn is nil", bn.localNode.Addr(), r)
		return nil
	}
	pack, err := r.Pack()
	if err != nil {
		bn.log.E("[net] pack data encountered err:%v", err)
		return nil
	}

	conn.SetWriteDeadline(time.Now().Add(800 * time.Millisecond))
	_, err = conn.Write(pack)
	if err != nil {
		bn.log.E("[net] conn write got err:%v", err)
		conn.Close()
	}
	return err
}
func (bn *BaseNetwork) readMsg(conn net.Conn) ([]byte, error) {

	for {

		length := int32(0)
		revH := make([]byte, 4)
		revL := make([]byte, 4)
		if _, err := io.ReadFull(conn, revH); err != nil {
			return nil, err
		}

		if !isNetVersionMatch(revH) {
			return nil, errors.New("[net] Receive head error")
		}

		if _, err := io.ReadFull(conn, revL); err != nil {
			return nil, err
		}

		if err := binary.Read(bytes.NewReader(revL), binary.BigEndian, &length); err != nil {
			return nil, err
		}

		rbuf := make([]byte, length+8)
		var n int
		var err error
		var rLen int

		for {

			if n, err = io.ReadFull(conn, rbuf[rLen+8:]); err != nil {
				return nil, err
			}

			if n != len(rbuf)-8 {
				rLen += n
				continue
			} else {
				break
			}

		}

		copy(rbuf[0:4], revH)
		copy(rbuf[4:8], revL)

		return rbuf, nil
	}

}
func (bn *BaseNetwork) receiveLoop(conn net.Conn) {

	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()

	for {

		buf, err := bn.readMsg(conn)
		if err != nil {
			log.Log.E("[net] readMsg error:%v", err)
			return
		}

		req := new(Request)
		if err := req.Unpack(bytes.NewReader(buf)); err != nil {
			log.Log.E("[net] req.Unpack error")
			continue
		}

		req.handle(bn, conn)

	}

}

// AllNodesExcludeAddr returns all the known node in the network.
func (bn *BaseNetwork) AllNodesExcludeAddr(excludeAddr string) ([]string, error) {
	if bn.nodeTable == nil {
		return nil, nil
	}
	addrs := make([]string, 0)
	iter := bn.nodeTable.NewIterator()
	for iter.Next() {
		addr := string(iter.Key())
		if addr != excludeAddr {
			addrs = append(addrs, addr)
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return nil, err
	}

	return addrs, nil
}

// putnode puts node into node table of server.
func (bn *BaseNetwork) putNode(addrs string) {
	if addrs == "" {
		return
	}
	addrArr := strings.Split(addrs, ",")
	for _, addr := range addrArr {
		node, err := discover.ParseNode(addr)
		if err != nil {
			bn.log.E("failed to ParseNode  %v,err: %v", addr, err)
			continue
		}
		if addr != "" && addr != bn.localNode.Addr() {
			_, err := bn.nodeTable.Has([]byte(addr))
			if err != nil {
				bn.log.E("failed to nodetable has %v, err: %v", addr, err)
				continue
			}
			bn.nodeTable.Put([]byte(node.Addr()), common.IntToBytes(NodeLiveCycle))
			if _, exist := bn.NodeAddedTime.Load(node.Addr()); !exist {
				bn.NodeAddedTime.Store(node.Addr(), time.Now().Unix())
			}
		}
	}
	bn.findNeighbours()
	return
}

// nodeCheckLoop inspections last registration time of node.
func (bn *BaseNetwork) nodeCheckLoop() {
	if bn.localNode.TCP == RegisterServerPort {
		for {
			iter := bn.nodeTable.NewIterator()
			for iter.Next() {
				k := iter.Key()
				v := common.BytesToInt(iter.Value())
				if v <= 0 {
					bn.log.D("[net] delete node %v, cuz its last register time is %v", string(iter.Key()), common.BytesToInt64(iter.Value()))
					bn.nodeTable.Delete(iter.Key())
					bn.peers.RemoveByNodeStr(string(iter.Key()))
					bn.neighbours.Delete(string(iter.Key()))
					bn.NodeAddedTime.Delete(string(iter.Key()))
				} else {
					bn.nodeTable.Put(k, common.IntToBytes(v-1))
				}
			}
			time.Sleep(CheckKnownNodeInterval * time.Second)
		}
	}

}

// registerLoop registers local address to boot nodes.
func (bn *BaseNetwork) registerLoop() {
	for {
		if bn.localNode.TCP != RegisterServerPort && bn.regAddr != "" {
			peer, err := bn.dial(bn.regAddr)
			if err != nil {
				bn.log.E("[net] failed to connect boot node, err:%v", err)
				time.Sleep(CheckKnownNodeInterval * time.Second)
				continue
			}
			bn.log.D("[net] %v request node table from %v", bn.localNode.Addr(), bn.regAddr)
			req := newRequest(ReqNodeTable, bn.localNode.Addr(), nil)
			if er := bn.send(peer.conn, req); er != nil {
				bn.peers.RemoveByNodeStr(bn.regAddr)
			}
		}
		time.Sleep(CheckKnownNodeInterval * time.Second)
	}
}

// findNeighbours finds neighbour nodes in the node table.
func (bn *BaseNetwork) findNeighbours() {
	nodesStr, _ := bn.AllNodesExcludeAddr(bn.localNode.Addr())
	nodes := make([]*discover.Node, 0)
	for _, nodeStr := range nodesStr {
		node, _ := discover.ParseNode(nodeStr)
		nodes = append(nodes, node)
	}
	neighbours := bn.localNode.FindNeighbours(nodes)

	bn.neighbours.Range(func(k, v interface{}) bool {
		bn.neighbours.Delete(k)
		return true
	})

	for _, n := range neighbours {
		bn.neighbours.Store(n.String(), n)
	}
}

// AskABlock asks a node for a block.
func (bn *BaseNetwork) AskABlock(height uint64, to string) error {
	msg := message.Message{
		Body:    common.Uint64ToBytes(height),
		ReqType: int32(ReqDownloadBlock),
		From:    bn.localNode.Addr(),
		Time:    time.Now().UnixNano(),
		To:      to,
	}
	bn.Send(msg)
	return nil
}

// QueryBlockHash queries blocks' hash by broadcast.
func (bn *BaseNetwork) QueryBlockHash(start, end uint64) error {
	hr := message.BlockHashQuery{Start: start, End: end}
	bytes, err := hr.Marshal(nil)
	if err != nil {
		bn.log.D("marshal BlockHashQuery failed. err=%v", err)
		return err
	}
	msg := message.Message{
		Body:    bytes,
		ReqType: int32(BlockHashQuery),
		TTL:     1, //BlockHashQuery req just broadcast to its neibour
		From:    bn.localNode.Addr(),
		Time:    time.Now().UnixNano(),
	}
	bn.log.D("[net] query block hash. start=%v, end=%v, from=%v", start, end, bn.localNode.Addr())
	bn.randomBroadcast(msg)
	return nil
}

// Download downloads blocks by height.
func (bn *BaseNetwork) Download(start, end uint64) error {
	for i := start; i <= end; i++ {
		bn.DownloadHeights.Store(uint64(i), 0)
	}

	for retry := 0; retry < MaxDownloadRetry; retry++ {
		wg := sync.WaitGroup{}
		time.Sleep(time.Duration(retry) * time.Second)
		bn.DownloadHeights.Range(func(k, v interface{}) bool {
			downloadHeight, ok1 := k.(uint64)
			retryTimes, ok2 := v.(int)
			if !ok1 || !ok2 {
				return true
			}
			if retryTimes > MaxDownloadRetry {
				return true
			}
			msg := message.Message{
				Body:    common.Uint64ToBytes(downloadHeight),
				ReqType: int32(ReqDownloadBlock),
				TTL:     MsgMaxTTL,
				From:    bn.localNode.Addr(),
				Time:    time.Now().UnixNano(),
			}
			bn.log.D("[net] download height = %v  nodeMap = %v", downloadHeight, bn.NodeHeightMap)
			bn.DownloadHeights.Store(downloadHeight, retryTimes+1)
			wg.Add(1)
			go func() {
				bn.Broadcast(msg)
				wg.Done()
			}()
			return true
		})

		wg.Wait()
	}
	return nil
}

// CancelDownload cancels downloading block with height between start and end.
func (bn *BaseNetwork) CancelDownload(start, end uint64) error {
	for ; start <= end; start++ {
		bn.DownloadHeights.Delete(start)
	}
	return nil
}

// SetNodeHeightMap sets a node's block height.
func (bn *BaseNetwork) SetNodeHeightMap(nodeStr string, height uint64) {
	bn.lock.Lock()
	defer bn.lock.Unlock()
	bn.NodeHeightMap[nodeStr] = height
}

// GetNodeHeightMap gets a node's block height.
func (bn *BaseNetwork) GetNodeHeightMap(nodeStr string) uint64 {
	bn.lock.Lock()
	defer bn.lock.Unlock()
	return bn.NodeHeightMap[nodeStr]
}

func randNodeMatchHeight(m map[string]uint64, downloadHeight uint64) (targetNode string) {
	rand.Seed(time.Now().UnixNano())
	matchNum := 1
	for nodeStr, height := range m {
		if height >= downloadHeight {
			randNum := rand.Int31n(int32(matchNum))
			if randNum == 0 {
				targetNode = nodeStr
			}
			matchNum++
		}
	}
	return targetNode
}

// recentSentLoop cleans up recent sent time.
func (bn *BaseNetwork) recentSentLoop() {
	for {
		bn.log.D("[net] clean up recent sent loop")
		now := time.Now()
		bn.RecentSent.Range(func(k, v interface{}) bool {
			data, ok1 := k.(string)
			t, ok2 := v.(time.Time)
			if !ok1 || !ok2 {
				return true
			}
			if t.Add(MsgLiveThresholdSeconds * time.Second).Before(now) {
				bn.RecentSent.Delete(data)
			}
			return true
		})

		time.Sleep(MsgLiveThresholdSeconds * time.Second)
	}
}

func (bn *BaseNetwork) isRecentSent(msg message.Message) bool {
	msg.TTL = 0
	data, err := msg.Marshal(nil)
	if err != nil {
		bn.log.E("[net] marshal request encountered err:%v", err)
	}
	h := string(common.Sha256(data))

	if _, ok := bn.RecentSent.Load(h); !ok {
		bn.RecentSent.Store(h, time.Now())
		return false
	}
	return true
}

func (bn *BaseNetwork) sendNodeTable(from []byte, conn net.Conn) {
	bn.log.D("[net] req node table from: %s", from)
	addrs, err := bn.AllNodesExcludeAddr(string(from))
	if err != nil {
		bn.log.E("[net] failed to get node table, %v", err)
	}
	req := newRequest(NodeTable, bn.localNode.Addr(), []byte(strings.Join(addrs, ",")))
	if er := bn.send(conn, req); er != nil {
		bn.log.E("[net] failed to send node table,%v ", err)
		conn.Close()
	}
	return
}

func prometheusSendBlockTx(req message.Message) {
	if req.ReqType == int32(ReqPublishTx) {
		// sendTransactionSize.Observe(float64(req.Size()))
		sendTransactionCount.Inc()
	}
	if req.ReqType == int32(ReqNewBlock) {
		// sendBlockSize.Observe(float64(req.Size()))
		sendBlockCount.Inc()
	}
}
