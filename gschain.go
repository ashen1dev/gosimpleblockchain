package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var http_port = os.Getenv("HTTP_PORT")
var p2p_port = os.Getenv("P2P_PORT")
var initialPeers []string = strings.Split(os.Getenv("PEERS"), ",")

type Block struct {
	index        int
	previousHash string
	timestamp    string
	data         string
	hash         string
}

type Blocks []Block

func (bs Blocks) Len() int {
	return len(bs)
}

func (bs Blocks) Swap(i, j int) {
	bs[i], bs[j] = bs[j], bs[i]
}

func (bs Blocks) Less(i, j int) bool {
	return bs[i].index < bs[j].index
}

type Message struct {
	mtype int `json:"type"`
	data  string
}

var sockets []*websocket.Conn

const (
	QUERY_LATEST = 0 + iota
	QUERY_ALL
	RESPONSE_BLOCKCHAIN
)

var GenesisBlock = Block{0, "0", "1465154705", "my genesis block!!", "816534932c2b7154836da6afc367695e6337db8a921823784c14378abed4f7d7"}
var blockchain = []Block{GenesisBlock}

func initHttpServer() {
	r := mux.NewRouter()
	r.HandleFunc("/blocks", func(w http.ResponseWriter, r *http.Request) {
		rawdata, _ := json.Marshal(blockchain)
		w.Write(rawdata)
	})
	r.HandleFunc("/mineBlock", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "can't read body", http.StatusBadRequest)
			return
		}
		newBlock := generateNextBlock(string(body))
		addBlock(newBlock)
		broadcast(responseLatestMsg())
		log.Fatal("block added: ", newBlock)
	})
	r.HandleFunc("/peers", func(w http.ResponseWriter, r *http.Request) {
		//w.Write([]byte(strings.Join(sockets[:], ",")))
	})
	r.HandleFunc("/addPeer", func(w http.ResponseWriter, r *http.Request) {
		var temppeers []string
		body, err := ioutil.ReadAll(r.Body)
		err = json.Unmarshal(body, temppeers)
		if err != nil {
			log.Fatal("peer add failed")
		} else {
			connectToPeers(temppeers)
			log.Fatal("peer added: ", temppeers)
		}
	})
	r.HandleFunc("/ws", wsHandler)
	http.Handle("/", r)
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "http://"+r.Host {
		http.Error(w, "Origin not allowed", 403)
		return
	}
	conn, err := websocket.Upgrade(w, r, w.Header(), 1024, 1024)
	if err != nil {
		http.Error(w, "Could not open websocket connection", http.StatusBadRequest)
	}

	MessageHnalder(conn)
}

func MessageHnalder(conn *websocket.Conn) {
	var input Message
	conn.WriteJSON(queryChainLengthMsg())

	for {
		err := conn.ReadJSON(&input)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return
			}
			log.Fatalf("%v", err)
			return
		}

		switch input.mtype {
		case QUERY_LATEST:
			conn.WriteJSON(responseLatestMsg())
			break
		case QUERY_ALL:
			conn.WriteJSON(responseChainMsg())
			break
		case RESPONSE_BLOCKCHAIN:
			handleBlockChainResponse([]byte(input.data))
			break
		}
	}
}

func generateNextBlock(blockData string) *Block {
	nextblock := &Block{
		index:        getLatestBlock().index + 1,
		previousHash: getLatestBlock().hash,
		timestamp:    time.Now().String(),
		data:         blockData,
	}
	nextblock.hash = calculateHash(nextblock.index, nextblock.previousHash, nextblock.timestamp, nextblock.data)
	return nextblock
}

func calculateHashForBlock(block *Block) string {
	return calculateHash(block.index, block.previousHash, block.timestamp, block.data)
}

func calculateHash(index int, previousHash string, timestamp string, data string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d%s%s%s", index, previousHash, timestamp, data)))
	return string(sum[:32])
}

func addBlock(newBlock *Block) {
	if isValidNewBlock(newBlock, getLatestBlock()) {
		blockchain = append(blockchain, *newBlock)
	}
}

func isValidNewBlock(newBlock, previousBlock *Block) bool {
	if previousBlock.index+1 != newBlock.index {
		log.Fatal("Wrong Block: invalid index")
		return false
	} else if previousBlock.hash != newBlock.previousHash {
		log.Fatal("Wrong Block: invalid previousHash")
		return false
	} else if calculateHashForBlock(newBlock) != newBlock.hash {
		log.Fatal("Wrong Block: invalid hash: ", calculateHashForBlock(newBlock), newBlock.hash)
		return false
	}
	return true
}

func connectToPeers(newPeers []string) {
	for _, peernode := range newPeers {
		go func(peernode string) {
			/* TODO: websocket connection handle */
		}(peernode)
	}
}

func handleBlockChainResponse(message []byte) {
	var blocks Blocks
	err := json.Unmarshal(message, &blocks)
	if err != nil {
		log.Fatal("Ignored")
		return
	}
	sort.Sort(Blocks(blocks))
	latestBlockReceived := blocks[len(blocks)-1]
	latestBlockHeld := getLatestBlock()
	if latestBlockReceived.index > latestBlockHeld.index {
		log.Fatal("Blockchain Possibly Behind. We got: ", latestBlockHeld.index, " Peer got: ", latestBlockReceived.index)
		if latestBlockHeld.hash == latestBlockReceived.previousHash {
			log.Fatal("Append Block to Chain")
			blockchain = append(blockchain, latestBlockReceived)
			broadcast(responseLatestMsg())
		} else if len(blocks) == 1 {
			broadcast(queryAllMsg())
		} else {
			replaceChain(blocks)
		}
	} else {
		log.Fatal("Ignored")
	}
}

func replaceChain(newBlocks []Block) {
	if isValidChain(newBlocks) && len(newBlocks) > len(blockchain) {
		log.Fatal("Received blockchain valid")
		blockchain = newBlocks
		broadcast(responseLatestMsg())
	} else {
		log.Fatal("Received blockchain invalid")
	}
}

func isValidChain(blockchainToValidate []Block) bool {
	njson, _ := json.Marshal(blockchainToValidate[0])
	ejson, _ := json.Marshal(GenesisBlock)
	if bytes.Compare(njson, ejson) != 0 {
		return false
	}
	tempBlocks := []Block{blockchainToValidate[0]}
	for i := 1; i < len(blockchainToValidate); i++ {
		if isValidNewBlock(&blockchainToValidate[i], &tempBlocks[i-1]) {
			tempBlocks = append(tempBlocks, blockchainToValidate[i])
		} else {
			return false
		}
	}
	return true
}

func getLatestBlock() *Block {
	return &blockchain[len(blockchain)-1]
}

func queryChainLengthMsg() string {
	return fmt.Sprintf("{'type': %d}", QUERY_LATEST)
}

func queryAllMsg() string {
	return fmt.Sprintf("{'type': %d}", QUERY_ALL)
}

func responseChainMsg() string {
	bcjson, err := json.Marshal(blockchain)
	if err != nil {
		log.Fatal("Failed to marshal chain")
		return ""
	}
	return fmt.Sprintf("{'type': %d, 'data': %s}", RESPONSE_BLOCKCHAIN, string(bcjson))
}

func responseLatestMsg() string {
	lbjson, err := json.Marshal(getLatestBlock())
	if err != nil {
		log.Fatal("Failed to marshal last block")
		return ""
	}
	return fmt.Sprintf("{'type': %d, 'data': %s}", RESPONSE_BLOCKCHAIN, string(lbjson))
}

func broadcast(message string) {
	for _, conn := range sockets {
		conn.WriteJSON(message)
	}
}

func main() {
	initHttpServer()
	connectToPeers(initialPeers)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
