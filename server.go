package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/garyburd/redigo/redis"
	_ "github.com/lib/pq"
	"github.com/streadway/amqp"
)

//	Globals
var (
	config = func() transactionConfig {
		if runningInDocker() {
			return loadConfigDocker()
		} else {
			return loadConfigLocal()
		}
	}()

	Pool                 *redis.Pool
	db                   = loadDB()
	buyMap               = new(sync.Map)
	buyTriggerMap        = new(sync.Map)
	sellMap              = new(sync.Map)
	sellTriggerMap       = new(sync.Map)
	sellTriggerStockMap  = new(sync.Map)
	buyTriggerStockMap   = new(sync.Map)
	buyTriggerTickerMap  = new(sync.Map)
	sellTriggerTickerMap = new(sync.Map)
	aggBuy               = make(chan string)
	aggSell              = make(chan string)
	rmqConn              *amqp.Connection
	transactionChannel   = make(chan interface{})
	errorChannel         = make(chan interface{})
	userChannel          = make(chan interface{})
	quoteChannel         = make(chan interface{})

	SERVER   = "1"
	FILENAME = "10userWorkLoad"
)

type Quote struct {
	Price       int
	StockSymbol string
	UserId      string
	Timestamp   int64
	CryptoKey   string
	Cached      bool
}

type GetQuote struct {
	UserId      string
	StockSymbol string
}

func loadConfigDocker() transactionConfig {
	newConfig := transactionConfig{}
	newConfig.auditServer = os.Getenv("AUDIT_SERVER_HOST")
	newConfig.quoteServer = os.Getenv("QUOTE_SERVER_HOST")
	newConfig.quotePort = os.Getenv("QUOTE_SERVER_PORT")
	newConfig.db = os.Getenv("TX_POSTGRES_HOST")
	newConfig.port = os.Getenv("TX_SERVER_PORT")
	newConfig.rabbitMQ = os.Getenv("TX_RABBITMQ_CONN_STRING")
	newConfig.redisHost = os.Getenv("TX_REDIS_HOST")
	newConfig.redisPort = os.Getenv("TX_REDIS_PORT")
	return newConfig
}

func loadConfigLocal() transactionConfig {
	// for my friend geoff
	newConfig := transactionConfig{}
	newConfig.auditServer = "localhost"
	newConfig.quoteServer = "localhost"
	newConfig.quotePort = ":44418"
	newConfig.db = "localhost"
	newConfig.port = ":44416"
	newConfig.rabbitMQ = "amqp://guest:guest@audit-mq:5672/"
	newConfig.redisHost = "redis-ts"
	newConfig.redisPort = ":6379"
	return newConfig
}

func getQuote(stockSymbol string, userId string, transactionNum int) (Quote, error) {
	q := GetQuote{}
	q.UserId = userId
	q.StockSymbol = stockSymbol
	jsonValue, _ := json.Marshal(q)
	resp, err := http.Post("http://"+config.quoteServer+":"+config.quotePort+"/quote", "application/json", bytes.NewBuffer(jsonValue))
	failOnError(err, "Error sending request")
	defer resp.Body.Close()

	if err != nil {
		fmt.Println("Connection error")
		return Quote{}, err
	}

	decoder := json.NewDecoder(resp.Body)
	req := struct {
		Price       string
		StockSymbol string
		UserId      string
		Timestamp   int64
		CryptoKey   string
		Cached      bool
	}{"", "", "", 0, "", false}

	err = decoder.Decode(&req)

	if !req.Cached {
		//only audit uncached events
		auditEvent := QuoteServer{Server: SERVER, Price: floatStringToCents(req.Price), StockSymbol: req.StockSymbol, Username: req.UserId, QuoteServerTime: req.Timestamp, Cryptokey: req.CryptoKey, TransactionNum: transactionNum}
		audit(auditEvent)
	}

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "QUOTE", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: transactionNum}
		audit(auditError)
		//failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return Quote{}, err
	}

	thisQuote := Quote{}

	thisQuote.Price = floatStringToCents(req.Price)
	thisQuote.StockSymbol = req.StockSymbol
	thisQuote.UserId = req.UserId
	thisQuote.Timestamp = req.Timestamp
	thisQuote.CryptoKey = req.CryptoKey

	return thisQuote, nil
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Transaction server connection successful")
}

func quoteHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		TransactionNum int
	}{"", "", 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "QUOTE", Username: req.UserId, StockSymbol: req.StockSymbol, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) > 3 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "QUOTE", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Stock symbol string too long", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	newQuote, err := getQuote(req.StockSymbol, req.UserId, req.TransactionNum)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "QUOTE", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error receiving quote", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, err = json.Marshal(newQuote)
	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "QUOTE", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error reading quote", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	//fmt.Fprintf(w, string(quoteJson))
}

func addHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		Amount         int
		TransactionNum int
	}{"", 0, 1}

	err := decoder.Decode(&req)

	auditEvent := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEvent)

	if err != nil || req.Amount < 0 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	err = writeFundsThroughCache(req.UserId, req.Amount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error writing funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func buyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "BUY", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) < 3 || req.Amount < 0 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	buyTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

	err = writeFundsThroughCache(req.UserId, 0-req.Amount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error removing funds for buy", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Get a quote
	newQuote, err := getQuote(req.StockSymbol, req.UserId, req.TransactionNum)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error getting quote", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	// Parse Quote
	thisBuy := Buy{}

	thisBuy.BuyTimestamp = buyTime
	thisBuy.QuoteTimestamp = newQuote.Timestamp
	thisBuy.QuoteCryptoKey = newQuote.CryptoKey
	thisBuy.StockSymbol = newQuote.StockSymbol
	thisBuy.StockPrice = newQuote.Price
	thisBuy.BuyAmount = req.Amount

	//	Add buy to stack of pending buys
	userBuyStack, _ := buyMap.LoadOrStore(req.UserId, &Stack{})
	userBuyStack.(Stacker).Push(thisBuy)

	//	Send response back to client
	w.WriteHeader(http.StatusOK)

}

func cancelBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 1}

	err := decoder.Decode(&req)

	userBuyStack, _ := buyMap.Load(req.UserId)

	if err != nil || userBuyStack == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)
		return
	}

	latestBuy := userBuyStack.(Stacker).Pop()

	if latestBuy == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)
		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_BUY", Username: req.UserId, StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	err = writeFundsThroughCache(req.UserId, latestBuy.(Buy).BuyAmount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: "0", Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "No pending BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	//audit(auditEventA)

	w.WriteHeader(http.StatusOK)
}

func confirmBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 1}

	err := decoder.Decode(&req)

	userBuyStack, _ := buyMap.Load(req.UserId)

	if err != nil || userBuyStack == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending buy", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)

		return
	}

	latestBuy := userBuyStack.(Stacker).Pop()

	if latestBuy == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending buy", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)

		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_BUY", Username: req.UserId, StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Calculate actual cost of buy
	stockQuantity := int(latestBuy.(Buy).BuyAmount / int(latestBuy.(Buy).StockPrice*100))
	actualCharge := int(latestBuy.(Buy).StockPrice*100) * stockQuantity
	refundAmount := latestBuy.(Buy).BuyAmount - actualCharge

	err = writeFundsThroughCache(req.UserId, refundAmount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: "0", Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Couldnt refund extra buy funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: refundAmount, TransactionNum: req.TransactionNum}
	//audit(auditEventA)

	err = writeStocksThroughCache(req.UserId, latestBuy.(Buy).StockSymbol, stockQuantity)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//	Return resp to client
	w.WriteHeader(http.StatusOK)
}

func sellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "SELL", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) < 3 || req.Amount < 0 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	sellTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

	//	Get a quote
	newQuote, err := getQuote(req.StockSymbol, req.UserId, req.TransactionNum)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error getting quote", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//	Parse quote
	thisSell := Sell{}

	thisSell.SellTimestamp = sellTime
	thisSell.QuoteTimestamp = newQuote.Timestamp
	thisSell.QuoteCryptoKey = newQuote.CryptoKey
	thisSell.StockSymbol = newQuote.StockSymbol
	thisSell.StockPrice = newQuote.Price
	thisSell.SellAmount = req.Amount
	thisSell.StockSellAmount = int(math.Ceil(float64(req.Amount) / float64(thisSell.StockPrice)))

	if thisSell.StockSellAmount < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "No stocks to sell", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	err = writeStocksThroughCache(req.UserId, thisSell.StockSymbol, 0-thisSell.StockSellAmount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//	Add sell to stack of pending sells
	userSellStack, _ := sellMap.LoadOrStore(req.UserId, &Stack{})
	userSellStack.(Stacker).Push(thisSell)

	w.WriteHeader(http.StatusOK)
}

func cancelSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 1}

	err := decoder.Decode(&req)

	userSellStack, _ := sellMap.Load(req.UserId)

	if err != nil || userSellStack == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)
		return
	}

	latestSell := userSellStack.(Stacker).Pop()

	if latestSell == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)
		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SELL", Username: req.UserId, StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	err = writeStocksThroughCache(req.UserId, latestSell.(Sell).StockSymbol, latestSell.(Sell).StockSellAmount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not return stocks", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func confirmSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 1}

	err := decoder.Decode(&req)

	userSellStack, _ := sellMap.Load(req.UserId)

	if err != nil || userSellStack == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)
		return
	}

	latestSell := userSellStack.(Stacker).Pop()

	if latestSell == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		//auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		//audit(auditEventU)
		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_SELL", Username: req.UserId, StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Add funds to their account
	sellFunds := latestSell.(Sell).StockSellAmount * int(latestSell.(Sell).StockPrice*100)

	err = writeFundsThroughCache(req.UserId, sellFunds)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not update funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	//audit(auditEventA)

	w.WriteHeader(http.StatusOK)
}

func setBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "SET_BUY_AMOUNT", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) < 3 || req.Amount < 0 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Get time for new timestamp
	buyTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

	userBuyTrigger, _ := buyTriggerMap.Load(req.UserId + "," + req.StockSymbol)
	if userBuyTrigger != nil {
		err = writeFundsThroughCache(req.UserId, userBuyTrigger.(BuyTrigger).BuyAmount)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Unable to update trigger", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}
	}

	err = writeFundsThroughCache(req.UserId, 0-req.Amount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adjusting funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//	Create new BuyTrigger and add it to the map
	thisBuyTrigger := BuyTrigger{}

	thisBuyTrigger.BuyAmount = req.Amount
	thisBuyTrigger.SetBuyTimestamp = buyTime
	thisBuyTrigger.BuyPrice = -1
	thisBuyTrigger.StockSymbol = req.StockSymbol

	buyTriggerMap.Store(req.UserId+","+req.StockSymbol, thisBuyTrigger)

	//	Send response back to client
	w.WriteHeader(http.StatusOK)
}

func cancelSetBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		TransactionNum int
	}{"", "", 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SET_BUY", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) > 3 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//If there is actually an existing BuyTrigger
	userBuyTrigger, _ := buyTriggerMap.Load(req.UserId + "," + req.StockSymbol)
	if userBuyTrigger != nil {

		err = writeFundsThroughCache(req.UserId, userBuyTrigger.(BuyTrigger).BuyAmount)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Unable to return funds", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		//remove trigger also if it exists
		buyTriggerMap.Delete(req.UserId + "," + req.StockSymbol)

		removeBuyTimer(req.StockSymbol, req.UserId)

		w.WriteHeader(http.StatusOK)
		return
	}

	//cancelling when no trigger has been set
	auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request, no trigger set", TransactionNum: req.TransactionNum}
	failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
}

func setBuyTriggerHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "SET_BUY_TRIGGER", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) > 3 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Check if there is an existing trigger
	userBuyTrigger, _ := buyTriggerMap.Load(req.UserId + "," + req.StockSymbol)
	if userBuyTrigger != nil {
		newBuyTrigger := BuyTrigger{}
		newBuyTrigger.BuyAmount = req.Amount
		newBuyTrigger.BuyPrice = userBuyTrigger.(BuyTrigger).BuyPrice
		newBuyTrigger.SetBuyTimestamp = userBuyTrigger.(BuyTrigger).SetBuyTimestamp
		newBuyTrigger.StockSymbol = userBuyTrigger.(BuyTrigger).StockSymbol

		buyTriggerMap.Store(req.UserId+","+req.StockSymbol, newBuyTrigger)

		//timer meme
		addBuyTimer(req.StockSymbol, req.UserId)

		w.WriteHeader(http.StatusOK)
		return
	}

	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "No existing buy trigger", TransactionNum: req.TransactionNum}
	failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
}

func setSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "SET_SELL_AMOUNT", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) > 3 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	sellTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

	existingSellTrigger, _ := sellTriggerMap.Load(req.UserId + "," + req.StockSymbol)
	if existingSellTrigger != nil {
		//	return stocks
		err = writeStocksThroughCache(req.UserId, req.StockSymbol, existingSellTrigger.(SellTrigger).StockSellAmount)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error listing stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}
	}

	// update/create sell trigger
	thisSellTrigger := SellTrigger{}

	thisSellTrigger.SellAmount = req.Amount
	thisSellTrigger.SellPrice = -1
	thisSellTrigger.StockSymbol = req.StockSymbol
	thisSellTrigger.SetSellTimestamp = sellTime
	//  StockSellAmount cannot be figured out until the trigger point is set
	thisSellTrigger.StockSellAmount = 0

	sellTriggerMap.Store(req.UserId+","+req.StockSymbol, thisSellTrigger)

	w.WriteHeader(http.StatusOK)
}

func cancelSetSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		TransactionNum int
	}{"", "", 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SET_SELL", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || len(req.StockSymbol) > 3 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	existingSellTrigger, _ := sellTriggerMap.Load(req.UserId + "," + req.StockSymbol)
	if existingSellTrigger != nil {
		//	Return stocks to portfolio

		err = writeStocksThroughCache(req.UserId, req.StockSymbol, existingSellTrigger.(SellTrigger).StockSellAmount)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: existingSellTrigger.(SellTrigger).SellAmount, Username: req.UserId, ErrorMessage: "Error replacing stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		// remove the trigger
		sellTriggerMap.Delete(req.UserId + "," + req.StockSymbol)

		w.WriteHeader(http.StatusOK)
		return
	}

	//	Cancel with no existing trigger
	auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
	failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
}

func setSellTriggerHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 1}

	err := decoder.Decode(&req)

	auditEventU := UserCommand{Server: SERVER, Command: "SET_SELL_TRIGGER", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	if err != nil || req.Amount < 0 || len(req.StockSymbol) > 3 || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	existingSellTrigger, _ := sellTriggerMap.Load(req.UserId + "," + req.StockSymbol)
	if existingSellTrigger != nil {
		//	REMOVE THE MAXIMUM NUMBER OF STOCKS THAT COULD BE NEEDED TO FILL THIS SELL ORDER
		newSellTrigger := SellTrigger{}
		newSellTrigger.SellAmount = existingSellTrigger.(SellTrigger).SellAmount
		newSellTrigger.SellPrice = existingSellTrigger.(SellTrigger).SellPrice
		newSellTrigger.SetSellTimestamp = existingSellTrigger.(SellTrigger).SetSellTimestamp
		newSellTrigger.StockSymbol = existingSellTrigger.(SellTrigger).StockSymbol
		newSellTrigger.StockSellAmount = int(math.Ceil(float64(existingSellTrigger.(SellTrigger).SellAmount) / float64(req.Amount)))

		sellTriggerMap.Store(req.UserId+","+req.StockSymbol, newSellTrigger)

		if newSellTrigger.StockSellAmount < 0 {
			auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Not enough stocks to sell", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		err = writeStocksThroughCache(req.UserId, newSellTrigger.StockSymbol, 0-newSellTrigger.StockSellAmount)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		addSellTimer(req.StockSymbol, req.UserId)

		w.WriteHeader(http.StatusOK)
		return
	}

	//	no sell trigger
	auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "No existing sell trigger", TransactionNum: req.TransactionNum}
	failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
}

func displaySummaryHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 1}

	err := decoder.Decode(&req)

	auditEvent := UserCommand{Server: SERVER, Command: "DISPLAY_SUMMARY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
	audit(auditEvent)

	if err != nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "DISPLAY_SUMMARY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	w.WriteHeader(http.StatusOK)
	//fmt.Fprintf(w, "{\"funds\": %d}", userFunds)
}

func dumpLogHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		FileName       string
		TransactionNum int
		Server         string
	}{"", FILENAME, 1, SERVER}

	err := decoder.Decode(&req)

	auditEvent := UserCommand{Server: req.Server, Command: "DUMPLOG", Username: req.UserId, StockSymbol: "0", Filename: req.FileName, Funds: 0, TransactionNum: req.TransactionNum}
	audit(auditEvent)

	if err != nil || req.FileName == "" || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "DUMPLOG", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error fetching account information", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	if req.UserId == "" {
		fmt.Println("Dumplog of everything")
		jsonValue, _ := json.Marshal(req)
		resp, err := http.Post("http://"+config.auditServer+":44417/dumpLog", "application/json", bytes.NewBuffer(jsonValue))
		failOnError(err, "Error sending request")
		defer resp.Body.Close()
		w.WriteHeader(http.StatusOK)
		return
	}

	fmt.Println("Dumplog of only this users transactions")
	jsonValue, _ := json.Marshal(req)
	resp, err := http.Post("http://"+config.auditServer+":44417/dumpLog", "application/json", bytes.NewBuffer(jsonValue))
	failOnError(err, "Error sending request")
	defer resp.Body.Close()

	w.WriteHeader(http.StatusOK)
}

func addBuyTimer(s string, u string) {
	triggerList, _ := buyTriggerStockMap.Load(s)

	if triggerList == nil {
		newStringArray := make([]string, 0)
		buyTriggerStockMap.Store(s, newStringArray)
	}

	triggerList, _ = buyTriggerStockMap.Load(s)

	if len(triggerList.([]string)) == 0 {
		//new trigger added
		//set timer
		buyTriggerStockMap.Store(s, append(triggerList.([]string), u))

		ticker := time.NewTicker(time.Second * 60)
		buyTriggerTickerMap.Store(s, ticker)

		go func() {
			buyTicker, _ := buyTriggerTickerMap.Load(s)
			for range buyTicker.(*time.Ticker).C {
				aggBuy <- s
			}
		}()

	} else {
		var add = true
		thisStock, _ := buyTriggerStockMap.Load(s)
		for _, ele := range thisStock.([]string) {
			if ele == u {
				add = false
				break
			}
		}
		if add {
			buyTriggerStockMap.Store(s, append(thisStock.([]string), u))
		}
	}
}

func removeBuyTimer(s string, u string) {
	var na []string

	thisStock, _ := buyTriggerStockMap.Load(s)

	if thisStock == nil {
		newStringArray := make([]string, 0)
		buyTriggerStockMap.Store(s, newStringArray)
	}

	thisStock, _ = buyTriggerStockMap.Load(s)

	for _, v := range thisStock.([]string) {
		if v == u {
			continue
		} else {
			na = append(na, v)
		}
	}
	buyTriggerStockMap.Store(s, na)
	//fmt.Println("stopotpotptoptop", na, s, u)

	thisStock, _ = buyTriggerStockMap.Load(s)
	if len(thisStock.([]string)) == 0 {
		//fmt.Println("stopotpotptoptop")
		thisTicker, _ := buyTriggerTickerMap.Load(s)
		if thisTicker != nil {
			thisTicker.(*time.Ticker).Stop()
		}
		buyTriggerTickerMap.Delete(s)
	}
}

func addSellTimer(s string, u string) {
	stockList, _ := sellTriggerStockMap.Load(s)

	if stockList == nil {
		newStringArray := make([]string, 0)
		sellTriggerStockMap.Store(s, newStringArray)
	}

	stockList, _ = sellTriggerStockMap.Load(s)

	if len(stockList.([]string)) == 0 {
		//new trigger added
		//set timer
		sellTriggerStockMap.Store(s, append(stockList.([]string), u))

		ticker := time.NewTicker(time.Second * 60)
		sellTriggerTickerMap.Store(s, ticker)

		go func() {
			for range ticker.C {
				aggSell <- s
			}
		}()

	} else {
		var add = true
		for _, ele := range stockList.([]string) {
			if ele == u {
				add = false
				break
			}
		}
		if add {
			sellTriggerStockMap.Store(s, append(stockList.([]string), u))
		}
	}
}

func removeSellTimer(s string, u string) {
	var na []string

	thisStock, _ := sellTriggerStockMap.Load(s)
	for _, v := range thisStock.([]string) {
		if v == u {
			continue
		} else {
			na = append(na, v)
		}
	}
	sellTriggerStockMap.Store(s, na)

	thisStock, _ = sellTriggerStockMap.Load(s)
	if len(thisStock.([]string)) == 0 {
		thisTicker, _ := sellTriggerTickerMap.Load(s)
		thisTicker.(*time.Ticker).Stop()
		sellTriggerTickerMap.Delete(s)
	}
}

func loadDB() *sql.DB {

	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", config.db, 5432, "moonshot", "hodl", "moonshot")

	var db *sql.DB
	var err error
	db, err = sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		time.Sleep(time.Duration(i) * time.Second)

		if err = db.Ping(); err == nil {
			break
		}
		log.Println(err)
	}

	if err != nil {
		failGracefully(err, "Failed to open Postgres at "+config.db)
	}
	err = db.Ping()
	if err != nil {
		failGracefully(err, "Failed to Ping Postgres at "+config.db)
	} else {
		fmt.Println("Connected to DB at " + config.db)
	}
	return db
}

func monitorBuyTriggers() {
	go func() {
		for stockSymbol := range aggBuy {

			//polling transaction number set to 8011
			//	Get a quote
			triggerStock, _ := buyTriggerStockMap.Load(stockSymbol)
			if len(triggerStock.([]string)) > 0 {
				user := triggerStock.([]string)[0] //blame first user

				newQuote, err := getQuote(stockSymbol, user, 8011)

				if err != nil {
					//auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: 0, Username: user, ErrorMessage: "Error getting quote", TransactionNum: 8011}
					//audit(auditError)
					//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
					return
				}

				for _, UserId := range triggerStock.([]string) {
					//check for each user if the new stock value is what their trigger wants

					// Parse Quote
					thisBuy := Buy{}

					thisBuy.QuoteTimestamp = newQuote.Timestamp
					thisBuy.QuoteCryptoKey = newQuote.CryptoKey
					thisBuy.StockSymbol = newQuote.StockSymbol
					thisBuy.StockPrice = newQuote.Price
					buyTrigger, _ := buyTriggerMap.Load(UserId + "," + stockSymbol)
					thisBuy.BuyAmount = buyTrigger.(BuyTrigger).BuyPrice

					if int(thisBuy.StockPrice*100) <= thisBuy.BuyAmount {

						//we check the current value to see if the trigger goes right away
						// this is only for milestone 1

						//do confirm buy stuff

						//	Calculate actual cost of buy
						stockQuantity := int(buyTrigger.(BuyTrigger).BuyAmount / int(thisBuy.StockPrice*100))
						actualCharge := int(thisBuy.StockPrice*100) * stockQuantity
						refundAmount := buyTrigger.(BuyTrigger).BuyAmount - actualCharge

						err = writeFundsThroughCache(UserId, refundAmount)

						if err != nil {
							return
						}

						err = writeStocksThroughCache(UserId, stockSymbol, stockQuantity)

						if err != nil {
							return
						}

						//I assume the trigger goes away if you fufill it
						removeBuyTimer(stockSymbol, UserId)
					}
				}
			}
		}

	}()
}

func monitorSellTriggers() {
	go func() {
		for stockSymbol := range aggSell {
			//	Get a quote

			triggerStock, _ := sellTriggerStockMap.Load(stockSymbol)
			if len(triggerStock.([]string)) > 0 {
				user := triggerStock.([]string)[0] //blame it on the first user

				newQuote, err := getQuote(stockSymbol, user, 8011)

				if err != nil {
					//auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: 0, Username: user, ErrorMessage: "Error getting quote", TransactionNum: 8011}
					//audit(auditError)
					//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
					return
				}

				for _, UserId := range triggerStock.([]string) {
					// Parse Quote
					thisSell := Sell{}

					//thisBuy.BuyTimestamp = buyTime
					thisSell.QuoteTimestamp = newQuote.Timestamp
					thisSell.QuoteCryptoKey = newQuote.CryptoKey
					thisSell.StockSymbol = newQuote.StockSymbol
					thisSell.StockPrice = newQuote.Price
					sellTrigger, _ := sellTriggerMap.Load(UserId + "," + stockSymbol)
					if sellTrigger == nil {
						return
					}
					thisSell.SellAmount = sellTrigger.(SellTrigger).SellPrice

					if int(thisSell.StockPrice*100) >= thisSell.SellAmount {

						//	Add funds to their account
						sellFunds := sellTrigger.(SellTrigger).StockSellAmount * int(thisSell.StockPrice*100)

						err = writeFundsThroughCache(UserId, sellFunds)

						if err != nil {
							return
						}

						//auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: UserId, Funds: thisSell.SellAmount}
						//audit(auditEventA)

						removeSellTimer(stockSymbol, UserId)
					}
				}
			}

		}
	}()
}

func initRMQ() {

	var err error

	for i := 0; i < 5; i++ {
		time.Sleep(time.Duration(i) * time.Second)

		rmqConn, err = amqp.Dial(config.rabbitMQ)
		if err == nil {
			break
		}
		log.Println(err)
	}

	if err != nil {
		failOnError(err, "Failed to rmqConnect to RabbitMQ")
	}
}

func initDB() {
	redisHost := config.redisHost + config.redisPort
	Pool = newPool(redisHost)
	cleanupHook()
}

func cleanupHook() {

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)
	signal.Notify(c, syscall.SIGKILL)
	go func() {
		<-c
		Pool.Close()
		os.Exit(0)
	}()
}

func newPool(server string) *redis.Pool {

	return &redis.Pool{

		MaxIdle:     50,
		MaxActive:   10000,
		IdleTimeout: 30 * time.Second,

		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", server)
			if err != nil {
				return nil, err
			}
			return c, err
		},
	}
}

func main() {

	initDB()

	monitorSellTriggers()
	monitorBuyTriggers()

	rand.Seed(time.Now().Unix())

	initRMQ()
	defer rmqConn.Close()

	go ErrorAuditer(errorChannel)
	go UserAuditer(userChannel)
	go TransactionAuditer(transactionChannel)
	go QuoteAuditer(quoteChannel)

	go clearSells()
	go clearBuys()

	fmt.Printf("Listening on port %s\n", config.port)
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/quote", quoteHandler)
	http.HandleFunc("/add", addHandler)
	http.HandleFunc("/buy", buyHandler)
	http.HandleFunc("/cancelBuy", cancelBuyHandler)
	http.HandleFunc("/confirmBuy", confirmBuyHandler)
	http.HandleFunc("/sell", sellHandler)
	http.HandleFunc("/cancelSell", cancelSellHandler)
	http.HandleFunc("/confirmSell", confirmSellHandler)
	http.HandleFunc("/setBuy", setBuyHandler)
	http.HandleFunc("/cancelSetBuy", cancelSetBuyHandler)
	http.HandleFunc("/setBuyTrigger", setBuyTriggerHandler)
	http.HandleFunc("/setSell", setSellHandler)
	http.HandleFunc("/cancelSetSell", cancelSetSellHandler)
	http.HandleFunc("/setSellTrigger", setSellTriggerHandler)
	http.HandleFunc("/displaySummary", displaySummaryHandler)
	http.HandleFunc("/dumpLog", dumpLogHandler)
	http.ListenAndServe(config.port, nil)

}
