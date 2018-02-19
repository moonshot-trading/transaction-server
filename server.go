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
			return transactionConfig{"quote-server", "audit-server", "postgres"}
		} else {
			return transactionConfig{"localhost", "localhost", "localhost"}
		}
	}()

	Pool                 *redis.Pool
	quoteServerPort      = "44418"
	db                   = loadDB()
	buyMap               = make(map[string]*Stack)
	buyTriggerMap        = make(map[string]BuyTrigger)
	sellMap              = make(map[string]*Stack)
	sellTriggerMap       = make(map[string]SellTrigger)
	sellTriggerStockMap  = make(map[string][]string)
	buyTriggerStockMap   = make(map[string][]string)
	buyTriggerTickerMap  = make(map[string]*time.Ticker)
	sellTriggerTickerMap = make(map[string]*time.Ticker)
	aggBuy               = make(chan string)
	aggSell              = make(chan string)
	SERVER               = "1"
	FILENAME             = "10userWorkLoad"
	rmqConn              *amqp.Connection
	transactionChannel   = make(chan interface{})
	errorChannel         = make(chan interface{})
	userChannel          = make(chan interface{})
	quoteChannel         = make(chan interface{})
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

func getQuote(stockSymbol string, userId string, transactionNum int) (Quote, error) {

	// conn, err := net.Dial("tcp", "localhost:44415")
	// defer conn.Close()

	q := GetQuote{}
	q.UserId = userId
	q.StockSymbol = stockSymbol
	jsonValue, _ := json.Marshal(q)
	resp, err := http.Post("http://"+config.quoteServer+":"+quoteServerPort+"/quote", "application/json", bytes.NewBuffer(jsonValue))
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

	quoteJson, err := json.Marshal(newQuote)
	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "QUOTE", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error reading quote", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, string(quoteJson))
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
		fmt.Println("fdnbahsjflbdjalkbfdhjabfhjkadbhjk")
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//queryString := "INSERT INTO users(user_name, funds) VALUES($1, $2) ON CONFLICT (user_name) DO UPDATE SET funds = users.funds + $2"
	//stmt, err := db.Prepare(queryString)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adding funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// res, err := stmt.Exec(req.UserId, req.Amount)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adding funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// numRows, err := res.RowsAffected()

	// if numRows < 1 {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adding funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}
	_, rediserr := c.Do("INCRBY", req.UserId, req.Amount)

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adding funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
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

	//	Check if user has funds to buy at this price

	// queryString := "UPDATE users SET funds = users.funds - $1 WHERE user_name = $2"
	// stmt, err := db.Prepare(queryString)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Internal Server Error", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// res, err := stmt.Exec(req.Amount, req.UserId)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Could not reserve funds for BUY", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// numRows, err := res.RowsAffected()

	// if numRows < 1 {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Could not reserve funds for BUY", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(errors.New("Couldn't update account buy"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}
	res, rediserr := redis.Int(c.Do("GET", req.UserId))

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: "0", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "User does not exist", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	fmt.Println(res)

	if res-req.Amount < 0 {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "not enough money for BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, rediserr = c.Do("SET", req.UserId, res-req.Amount)

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Could not reserve funds for BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//	Audit removal of funds from account
	auditEventA := AccountTransaction{Server: SERVER, Action: "remove", Username: req.UserId, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventA)

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
	if buyMap[req.UserId] == nil {
		buyMap[req.UserId] = &Stack{}
	}

	buyMap[req.UserId].Push(thisBuy)

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

	if err != nil || buyMap[req.UserId] == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)
		return
	}

	latestBuy := buyMap[req.UserId].Pop()

	if latestBuy == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)
		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_BUY", Username: req.UserId, StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Return funds to account
	// queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	// stmt, err := db.Prepare(queryString)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error replacing funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// _, err = stmt.Exec(latestBuy.(Buy).BuyAmount, req.UserId)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error replacing funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}
	_, rediserr := c.Do("INCRBY", req.UserId, latestBuy.(Buy).BuyAmount)

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error replacing funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventA)

	w.WriteHeader(http.StatusOK)
}

func confirmBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 1}

	err := decoder.Decode(&req)

	if err != nil || buyMap[req.UserId] == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending buy", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)

		return
	}

	latestBuy := buyMap[req.UserId].Pop()

	if latestBuy == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending buy", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_BUY", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)

		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_BUY", Username: req.UserId, StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Calculate actual cost of buy
	stockQuantity := int(latestBuy.(Buy).BuyAmount / int(latestBuy.(Buy).StockPrice*100))
	actualCharge := int(latestBuy.(Buy).StockPrice*100) * stockQuantity
	refundAmount := latestBuy.(Buy).BuyAmount - actualCharge

	//	Put excess money back into account
	// queryString := "UPDATE users SET funds = users.funds + $1 WHERE user_name = $2"
	// stmt, err := db.Prepare(queryString)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// _, err = stmt.Exec(refundAmount, req.UserId)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}
	_, rediserr := c.Do("INCRBY", req.UserId, refundAmount)

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return

	}

	auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: refundAmount, TransactionNum: req.TransactionNum}
	audit(auditEventA)

	//	Give stocks to user
	queryString := "INSERT INTO stocks(user_name, stock_symbol, amount) VALUES($1, $2, $3) ON CONFLICT (user_name, stock_symbol) DO UPDATE SET amount = stocks.amount + $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, err = stmt.Exec(req.UserId, latestBuy.(Buy).StockSymbol, stockQuantity)

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

	//	Check if they have enough stock to sell at this price
	queryString := "UPDATE stocks SET amount = stocks.amount - $1 WHERE user_name = $2 AND stock_symbol = $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	res, err := stmt.Exec(thisSell.StockSellAmount, req.UserId, thisSell.StockSymbol)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
		failWithStatusCode(errors.New("Couldn't update portfolio sell"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//	Add sell to stack of pending sells
	if sellMap[req.UserId] == nil {
		sellMap[req.UserId] = &Stack{}
	}

	sellMap[req.UserId].Push(thisSell)

	w.WriteHeader(http.StatusOK)
}

func cancelSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 1}

	err := decoder.Decode(&req)

	if err != nil || sellMap[req.UserId] == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)
		return
	}

	latestSell := sellMap[req.UserId].Pop()

	if latestSell == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)
		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SELL", Username: req.UserId, StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Return stocks to portfolio
	queryString := "UPDATE stocks SET amount = amount + $1 WHERE user_name = $2 AND stock_symbol = $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not return stocks", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, err = stmt.Exec(latestSell.(Sell).StockSellAmount, req.UserId, latestSell.(Sell).StockSymbol)

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

	if err != nil || sellMap[req.UserId] == nil || req.TransactionNum < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)
		return
	}

	latestSell := sellMap[req.UserId].Pop()

	if latestSell == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

		auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_SELL", Username: req.UserId, StockSymbol: "0", Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)
		return
	}

	auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_SELL", Username: req.UserId, StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Add funds to their account
	sellFunds := latestSell.(Sell).StockSellAmount * int(latestSell.(Sell).StockPrice*100)

	// queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	// stmt, err := db.Prepare(queryString)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not update funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// _, err = stmt.Exec(sellFunds, req.UserId)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not update funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}
	_, rediserr := c.Do("INCRBY", req.UserId, sellFunds)

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not update funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventA)

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

	//	return the old buy funds if a buy already exists
	if _, exists := buyTriggerMap[req.UserId+","+req.StockSymbol]; exists {

		// queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
		// stmt, err := db.Prepare(queryString)

		// if err != nil {
		// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Unable to update trigger", TransactionNum: req.TransactionNum}
		// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		// 	return
		// }

		// _, err = stmt.Exec(buyTriggerMap[req.UserId+","+req.StockSymbol].BuyAmount, req.UserId)

		// if err != nil {
		// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Unable to update trigger", TransactionNum: req.TransactionNum}
		// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		// 	return
		// }

		c := Pool.Get()
		defer c.Close()

		if c == nil {
			fmt.Println("lol no db haha")
		}
		_, rediserr := c.Do("INCRBY", req.UserId, buyTriggerMap[req.UserId+","+req.StockSymbol].BuyAmount)
		if rediserr != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Unable to update trigger", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

	}

	//	Check if user has funds to buy at this price
	// queryString := "UPDATE users SET funds = users.funds - $1 WHERE user_name = $2"
	// stmt, err := db.Prepare(queryString)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adjusting funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// res, err := stmt.Exec(req.Amount, req.UserId)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adjusting funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// numRows, err := res.RowsAffected()

	// if numRows < 1 {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adjusting funds", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}
	res, rediserr := redis.Int(c.Do("GET", req.UserId))

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "User doesnt exist", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	if res-req.Amount < 0 {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adjusting funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, rediserr = c.Do("SET", req.UserId, res-req.Amount)

	if rediserr != nil {
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

	buyTriggerMap[req.UserId+","+req.StockSymbol] = thisBuyTrigger

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
	if existingBuyTrigger, exists := buyTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		// 	queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
		// 	stmt, err := db.Prepare(queryString)

		// 	if err != nil {
		// 		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Unable to return funds", TransactionNum: req.TransactionNum}
		// 		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		// 		return
		// 	}

		// 	_, err = stmt.Exec(existingBuyTrigger.BuyAmount, req.UserId)

		// 	if err != nil {
		// 		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Unable to return funds", TransactionNum: req.TransactionNum}
		// 		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		// 		return
		// 	}

		c := Pool.Get()
		defer c.Close()

		if c == nil {
			fmt.Println("lol no db haha")
		}
		_, rediserr := c.Do("INCRBY", req.UserId, existingBuyTrigger.BuyAmount)

		if rediserr != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Unable to return funds", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		//remove trigger also if it exists
		delete(buyTriggerMap, req.UserId+","+req.StockSymbol)

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
	if existingBuyTrigger, exists := buyTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		existingBuyTrigger.BuyPrice = req.Amount

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

	if existingSellTrigger, exists := sellTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		//	return stocks
		queryString := "UPDATE stocks SET amount = amount + $1 WHERE user_name = $2 AND stock_symbol = $3"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error listing stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		_, err = stmt.Exec(existingSellTrigger.StockSellAmount, req.UserId, req.StockSymbol)

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

	sellTriggerMap[req.UserId+","+req.StockSymbol] = thisSellTrigger

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

	if existingSellTrigger, exists := sellTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		//	Return stocks to portfolio
		queryString := "UPDATE stocks SET amount = amount + $1 WHERE user_name = $2  AND stock_symbol = $3"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: existingSellTrigger.SellAmount, Username: req.UserId, ErrorMessage: "Error replacing stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		_, err = stmt.Exec(existingSellTrigger.StockSellAmount, req.UserId, req.StockSymbol)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: existingSellTrigger.SellAmount, Username: req.UserId, ErrorMessage: "Error replacing stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		// remove the trigger
		delete(sellTriggerMap, req.UserId+","+req.StockSymbol)

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

	if existingSellTrigger, exists := sellTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		//	REMOVE THE MAXIMUM NUMBER OF STOCKS THAT COULD BE NEEDED TO FILL THIS SELL ORDER
		existingSellTrigger.StockSellAmount = int(math.Ceil(float64(existingSellTrigger.SellAmount) / float64(req.Amount)))

		if existingSellTrigger.StockSellAmount < 0 {
			auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Not enough stocks to sell", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		//	Check if they have enough stock to sell at this price
		queryString := "UPDATE stocks SET amount = stocks.amount - $1 WHERE user_name = $2 AND stock_symbol = $3"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		res, err := stmt.Exec(existingSellTrigger.StockSellAmount, req.UserId, existingSellTrigger.StockSymbol)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		numRows, err := res.RowsAffected()

		if numRows < 1 {
			auditError := ErrorEvent{Server: SERVER, Command: "SELL", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error allocating stocks", TransactionNum: req.TransactionNum}
			failWithStatusCode(errors.New("Couldn't update portfolio sell trig"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		addSellTimer(req.StockSymbol, req.UserId)

		w.WriteHeader(http.StatusOK)
		return
	}

	//	no sell trigger
	auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "No existing sell trigger", TransactionNum: req.TransactionNum}
	failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)

	w.WriteHeader(http.StatusOK)
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

	//	Get their current balance
	// queryString := "SELECT funds FROM users WHERE user_name = $1"

	// stmt, err := db.Prepare(queryString)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "DISPLAY_SUMMARY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error fetching account information", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
	// 	return
	// }

	// var userFunds int
	// err = stmt.QueryRow(req.UserId).Scan(&userFunds)

	// if err != nil {
	// 	auditError := ErrorEvent{Server: SERVER, Command: "DISPLAY_SUMMARY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error fetching account information", TransactionNum: req.TransactionNum}
	// 	failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
	// 	return
	// }

	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}

	userFunds, rediserr := redis.Int(c.Do("GET", req.UserId))

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "DISPLAY_SUMMARY", StockSymbol: "0", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error fetching account information", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "{\"funds\": %d}", userFunds)
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
	if len(buyTriggerStockMap[s]) == 0 {
		//new trigger added
		//set timer
		buyTriggerStockMap[s] = append(buyTriggerStockMap[s], u)

		ticker := time.NewTicker(time.Second * 60)
		buyTriggerTickerMap[s] = ticker

		go func() {
			for range buyTriggerTickerMap[s].C {
				aggBuy <- s
			}
		}()

	} else {
		var add = true
		for _, ele := range buyTriggerStockMap[s] {
			if ele == u {
				add = false
				break
			}
		}
		if add {
			buyTriggerStockMap[s] = append(buyTriggerStockMap[s], u)
		}
	}
}

func removeBuyTimer(s string, u string) {

	var na []string

	for _, v := range buyTriggerStockMap[s] {
		if v == u {
			continue
		} else {
			na = append(na, v)
		}
	}
	buyTriggerStockMap[s] = na
	fmt.Println("stopotpotptoptop", na, s, u)

	if len(buyTriggerStockMap[s]) == 0 {
		fmt.Println("stopotpotptoptop")
		if buyTriggerTickerMap[s] != nil {
			buyTriggerTickerMap[s].Stop()
		}
		delete(buyTriggerTickerMap, s)
	}
}

func addSellTimer(s string, u string) {
	if len(sellTriggerStockMap[s]) == 0 {
		//new trigger added
		//set timer
		sellTriggerStockMap[s] = append(sellTriggerStockMap[s], u)

		ticker := time.NewTicker(time.Second * 60)
		sellTriggerTickerMap[s] = ticker

		go func() {
			for range ticker.C {
				aggSell <- s
			}
		}()

	} else {
		var add = true
		for _, ele := range sellTriggerStockMap[s] {
			if ele == u {
				add = false
				break
			}
		}
		if add {
			sellTriggerStockMap[s] = append(sellTriggerStockMap[s], u)
		}
	}
}

func removeSellTimer(s string, u string) {

	var na []string

	for _, v := range sellTriggerStockMap[s] {
		if v == u {
			continue
		} else {
			na = append(na, v)
		}
	}
	sellTriggerStockMap[s] = na
	if len(sellTriggerStockMap[s]) == 0 {
		sellTriggerTickerMap[s].Stop()
		delete(sellTriggerTickerMap, s)
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
			if len(buyTriggerStockMap[stockSymbol]) > 0 {
				user := buyTriggerStockMap[stockSymbol][0] //blame first user

				newQuote, err := getQuote(stockSymbol, user, 8011)

				if err != nil {
					auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: 0, Username: user, ErrorMessage: "Error getting quote", TransactionNum: 8011}
					audit(auditError)
					//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
					return
				}

				for _, UserId := range buyTriggerStockMap[stockSymbol] {
					//check for each user if the new stock value is what their trigger wants

					// Parse Quote
					thisBuy := Buy{}

					//thisBuy.BuyTimestamp = buyTime
					thisBuy.QuoteTimestamp = newQuote.Timestamp
					thisBuy.QuoteCryptoKey = newQuote.CryptoKey
					thisBuy.StockSymbol = newQuote.StockSymbol
					thisBuy.StockPrice = newQuote.Price
					thisBuy.BuyAmount = buyTriggerMap[UserId+","+stockSymbol].BuyPrice
					//fmt.Println(thisBuy.StockSymbol, thisBuy.BuyAmount)
					if int(thisBuy.StockPrice*100) <= thisBuy.BuyAmount {

						//we check the current value to see if the trigger goes right away
						// this is only for milestone 1

						//do confirm buy stuff

						//	Calculate actual cost of buy
						stockQuantity := int(buyTriggerMap[UserId+","+stockSymbol].BuyAmount / int(thisBuy.StockPrice*100))
						actualCharge := int(thisBuy.StockPrice*100) * stockQuantity
						refundAmount := buyTriggerMap[UserId+","+stockSymbol].BuyAmount - actualCharge

						//	Put excess money back into account
						// queryString := "UPDATE users SET funds = users.funds + $1 WHERE user_name = $2"
						// stmt, err := db.Prepare(queryString)

						// if err != nil {
						// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: refundAmount, Username: UserId, ErrorMessage: "Error returning funds", TransactionNum: 8011}
						// 	//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
						// 	//fmt.Println("er -1")
						// 	audit(auditError)
						// 	return
						// }

						// _, err = stmt.Exec(refundAmount, UserId)

						// if err != nil {
						// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: refundAmount, Username: UserId, ErrorMessage: "Error returning funds", TransactionNum: 8011}
						// 	audit(auditError)
						// 	//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
						// 	//fmt.Println("er 0")
						// 	return
						// }

						c := Pool.Get()
						defer c.Close()

						if c == nil {
							fmt.Println("lol no db haha")
						}
						_, rediserr := c.Do("INCRBY", UserId, refundAmount)

						if rediserr != nil {
							auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: refundAmount, Username: UserId, ErrorMessage: "Error returning funds", TransactionNum: 8011}
							audit(auditError)
							return
						}

						//	Give stocks to user
						queryString := "INSERT INTO stocks(user_name, stock_symbol, amount) VALUES($1, $2, $3) ON CONFLICT (user_name, stock_symbol) DO UPDATE SET amount = stocks.amount + $3"
						stmt, err := db.Prepare(queryString)

						if err != nil {
							auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: stockQuantity, Username: UserId, ErrorMessage: "Error allocating stocks", TransactionNum: 8011}
							audit(auditError)
							//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
							//fmt.Println("er 1")
							return
						}

						_, err = stmt.Exec(UserId, stockSymbol, stockQuantity)

						if err != nil {
							auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: stockQuantity, Username: UserId, ErrorMessage: "Error allocating stocks", TransactionNum: 8011}
							//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
							//fmt.Println("er 2")
							audit(auditError)
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

			if len(sellTriggerStockMap[stockSymbol]) > 0 {
				user := sellTriggerStockMap[stockSymbol][1] //blame it on the first user

				newQuote, err := getQuote(stockSymbol, user, 8011)

				if err != nil {
					auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: stockSymbol, Filename: FILENAME, Funds: 0, Username: user, ErrorMessage: "Error getting quote", TransactionNum: 8011}
					audit(auditError)
					//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
					return
				}

				for _, UserId := range sellTriggerStockMap[stockSymbol] {
					// Parse Quote
					thisSell := Sell{}

					//thisBuy.BuyTimestamp = buyTime
					thisSell.QuoteTimestamp = newQuote.Timestamp
					thisSell.QuoteCryptoKey = newQuote.CryptoKey
					thisSell.StockSymbol = newQuote.StockSymbol
					thisSell.StockPrice = newQuote.Price
					thisSell.SellAmount = sellTriggerMap[UserId+","+stockSymbol].SellPrice

					if int(thisSell.StockPrice*100) >= thisSell.SellAmount {

						//	Add funds to their account
						sellFunds := sellTriggerMap[UserId+","+stockSymbol].StockSellAmount * int(thisSell.StockPrice*100)

						// queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
						// stmt, err := db.Prepare(queryString)

						// if err != nil {
						// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: thisSell.StockSymbol, Filename: FILENAME, Funds: thisSell.SellAmount, Username: UserId, ErrorMessage: "Error adding funds", TransactionNum: 8011}
						// 	audit(auditError)
						// 	//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
						// 	return
						// }

						// _, err = stmt.Exec(sellFunds, UserId)

						// if err != nil {
						// 	auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: thisSell.StockSymbol, Filename: FILENAME, Funds: thisSell.SellAmount, Username: UserId, ErrorMessage: "Error adding funds", TransactionNum: 8011}
						// 	audit(auditError)
						// 	//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
						// 	return
						// }

						c := Pool.Get()
						defer c.Close()

						if c == nil {
							fmt.Println("lol no db haha")
						}
						_, rediserr := c.Do("INCRBY", UserId, sellFunds)

						if rediserr != nil {
							auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: thisSell.StockSymbol, Filename: FILENAME, Funds: thisSell.SellAmount, Username: UserId, ErrorMessage: "Error adding funds", TransactionNum: 8011}
							audit(auditError)
							//failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
							return
						}

						auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: UserId, Funds: thisSell.SellAmount}
						audit(auditEventA)

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

		rmqConn, err = amqp.Dial("amqp://guest:guest@audit-mq:5672/")
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
	redisHost := "redis-ts" + ":6379" //TODO:make config
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

		MaxIdle:     80,
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

	port := ":44416"
	fmt.Printf("Listening on port %s\n", port)
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
	http.ListenAndServe(port, nil)

}
