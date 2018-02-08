package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

//	Globals
var (
	quoteServerURL  = "localhost"
	quoteServerPort = 44415
	db              = loadDB()
	buyMap          = make(map[string]*Stack)
	buyTriggerMap   = make(map[string]BuyTrigger)
	quoteMap        = make(map[string]Quote)
	sellMap         = make(map[string]*Stack)
	sellTriggerMap  = make(map[string]SellTrigger)
	SERVER          = "1"
	FILENAME        = "1userWorkLoad"
)

type Quote struct {
	Price       string
	StockSymbol string
	UserId      string
	Timestamp   int64
	CryptoKey   string
}

func getQuote(stockSymbol string, userId string, transactionNum int) (Quote, error) {
	quoteTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

	commandString := stockSymbol + "," + userId

	if cachedQuote, exists := quoteMap[stockSymbol]; exists {
		if cachedQuote.Timestamp+60000 > quoteTime {
			auditCacheEvent := SystemEvent{Server: SERVER, Command: "QUOTE", StockSymbol: stockSymbol, Username: userId, Filename: FILENAME, Funds: floatStringToCents(cachedQuote.Price), TransactionNum: transactionNum}
			audit(auditCacheEvent)
			return cachedQuote, nil
		}
	}

	conn, err := net.Dial("tcp", "localhost:44415")
	defer conn.Close()

	if err != nil {
		fmt.Println("Connection error")
		return Quote{}, err
	}

	conn.Write([]byte(commandString + "\n"))
	buff := make([]byte, 1024)
	length, _ := conn.Read(buff)

	quoteString := string(buff[:length])

	quoteStringComponents := strings.Split(quoteString, ",")
	thisQuote := Quote{}

	thisQuote.Price = quoteStringComponents[0]
	thisQuote.StockSymbol = quoteStringComponents[1]
	thisQuote.UserId = userId
	thisQuote.Timestamp, _ = strconv.ParseInt(quoteStringComponents[3], 10, 64)
	thisQuote.CryptoKey = strings.Replace(quoteStringComponents[4], "\n", "", 2)

	quoteMap[stockSymbol] = thisQuote

	auditEvent := QuoteServer{Server: SERVER, Price: floatStringToCents(thisQuote.Price), StockSymbol: thisQuote.StockSymbol, Username: thisQuote.UserId, QuoteServerTime: thisQuote.Timestamp, Cryptokey: thisQuote.CryptoKey, TransactionNum: transactionNum}
	audit(auditEvent)

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
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil || len(req.StockSymbol) > 3 {
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
	}{"", 0, 0}

	err := decoder.Decode(&req)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	queryString := "INSERT INTO users(user_name, funds) VALUES($1, $2) ON CONFLICT (user_name) DO UPDATE SET funds = users.funds + $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adding funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	res, err := stmt.Exec(req.UserId, req.Amount)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adding funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "ADD", StockSymbol: "", Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adding funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	auditEvent := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEvent)

	w.WriteHeader(http.StatusOK)
}

func buyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 0}

	err := decoder.Decode(&req)
	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	buyTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

	//	Check if user has funds to buy at this price
	queryString := "UPDATE users SET funds = users.funds - $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Internal Server Error", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	res, err := stmt.Exec(req.Amount, req.UserId)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Could not reserve funds for BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		auditError := ErrorEvent{Server: SERVER, Command: "BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Could not reserve funds for BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
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
	thisBuy.StockPrice, _ = strconv.ParseFloat(newQuote.Price, 64)
	thisBuy.BuyAmount = req.Amount

	//	Add buy to stack of pending buys
	if buyMap[req.UserId] == nil {
		buyMap[req.UserId] = &Stack{}
	}

	buyMap[req.UserId].Push(thisBuy)

	auditEventU := UserCommand{Server: SERVER, Command: "BUY", Username: req.UserId, StockSymbol: thisBuy.StockSymbol, Filename: FILENAME, Funds: thisBuy.BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Send response back to client
	w.WriteHeader(http.StatusOK)

}

func cancelBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 0}

	err := decoder.Decode(&req)

	if err != nil || buyMap[req.UserId] == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	latestBuy := buyMap[req.UserId].Pop()

	if latestBuy == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending BUY", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Return funds to account
	queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error replacing funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, err = stmt.Exec(latestBuy.(Buy).BuyAmount, req.UserId)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error replacing funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventA)

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_BUY", Username: req.UserId, StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	w.WriteHeader(http.StatusOK)
}

func confirmBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 0}

	err := decoder.Decode(&req)

	if err != nil || buyMap[req.UserId] == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending buy", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	latestBuy := buyMap[req.UserId].Pop()

	if latestBuy == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "No pending buy", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Calculate actual cost of buy
	stockQuantity := int(latestBuy.(Buy).BuyAmount / int(latestBuy.(Buy).StockPrice*100))
	actualCharge := int(latestBuy.(Buy).StockPrice*100) * stockQuantity
	refundAmount := latestBuy.(Buy).BuyAmount - actualCharge

	//	Put excess money back into account
	queryString := "UPDATE users SET funds = users.funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, err = stmt.Exec(refundAmount, req.UserId)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: refundAmount, TransactionNum: req.TransactionNum}
	audit(auditEventA)

	//	Give stocks to user
	queryString = "INSERT INTO stocks(user_name, stock_symbol, amount) VALUES($1, $2, $3) ON CONFLICT (user_name, stock_symbol) DO UPDATE SET amount = stocks.amount + $3"
	stmt, err = db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	fmt.Println("commit buy", req, latestBuy, stockQuantity)
	_, err = stmt.Exec(req.UserId, latestBuy.(Buy).StockSymbol, stockQuantity)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_BUY", StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, Username: req.UserId, ErrorMessage: "Error purchasing stock", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}
	auditEventU := UserCommand{Server: SERVER, Command: "COMMIT_BUY", Username: req.UserId, StockSymbol: latestBuy.(Buy).StockSymbol, Filename: FILENAME, Funds: latestBuy.(Buy).BuyAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

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
	}{"", "", 0, 0}

	err := decoder.Decode(&req)

	if err != nil {
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
	thisSell.StockPrice, _ = strconv.ParseFloat(newQuote.Price, 64)
	thisSell.SellAmount = req.Amount
	thisSell.StockSellAmount = int(math.Ceil(float64(req.Amount) / (thisSell.StockPrice * 100)))

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
		failWithStatusCode(errors.New("Couldn't update portfolio"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	//	Add sell to stack of pending sells
	if sellMap[req.UserId] == nil {
		sellMap[req.UserId] = &Stack{}
	}

	sellMap[req.UserId].Push(thisSell)

	auditEventU := UserCommand{Server: SERVER, Command: "SELL", Username: req.UserId, StockSymbol: thisSell.StockSymbol, Filename: FILENAME, Funds: thisSell.SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	w.WriteHeader(http.StatusOK)
}

func cancelSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 0}

	err := decoder.Decode(&req)

	if err != nil || sellMap[req.UserId] == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	latestSell := sellMap[req.UserId].Pop()

	if latestSell == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

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

	auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SELL", Username: req.UserId, StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	w.WriteHeader(http.StatusOK)
}

func confirmSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		TransactionNum int
	}{"", 0}

	err := decoder.Decode(&req)

	if err != nil || sellMap[req.UserId] == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	latestSell := sellMap[req.UserId].Pop()

	if latestSell == nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Add funds to their account
	sellFunds := latestSell.(Sell).StockSellAmount * int(latestSell.(Sell).StockPrice*100)

	queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not update funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	_, err = stmt.Exec(sellFunds, req.UserId)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "COMMIT_SELL", StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, Username: req.UserId, ErrorMessage: "Could not update funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	auditEventA := AccountTransaction{Server: SERVER, Action: "add", Username: req.UserId, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventA)

	auditEventU := UserCommand{Server: SERVER, Command: "CONFIRM_SELL", Username: req.UserId, StockSymbol: latestSell.(Sell).StockSymbol, Filename: FILENAME, Funds: latestSell.(Sell).SellAmount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	w.WriteHeader(http.StatusOK)
}

func setBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		Amount         int
		TransactionNum int
	}{"", "", 0, 0}

	err := decoder.Decode(&req)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Get time for new timestamp
	buyTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

	//	return the old buy funds if a buy already exists
	if _, exists := buyTriggerMap[req.UserId+","+req.StockSymbol]; exists {

		queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Unable to update trigger", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		_, err = stmt.Exec(buyTriggerMap[req.UserId+","+req.StockSymbol].BuyAmount, req.UserId)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Unable to update trigger", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}
	}

	//	Check if user has funds to buy at this price
	queryString := "UPDATE users SET funds = users.funds - $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adjusting funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	res, err := stmt.Exec(req.Amount, req.UserId)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_AMOUNT", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Error adjusting funds", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
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

	auditEventU := UserCommand{Server: SERVER, Command: "SET_BUY_AMOUNT", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	//	Send response back to client
	w.WriteHeader(http.StatusOK)
}

func cancelSetBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		TransactionNum int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	If there is actually an existing BuyTrigger
	if existingBuyTrigger, exists := buyTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Unable to return funds", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		_, err = stmt.Exec(existingBuyTrigger.BuyAmount, req.UserId)

		if err != nil {
			auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SET_BUY", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Unable to return funds", TransactionNum: req.TransactionNum}
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		//remove trigger also if it exists
		delete(buyTriggerMap, req.UserId+","+req.StockSymbol)

		auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SET_BUY", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)

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
	}{"", "", 0, 0}

	err := decoder.Decode(&req)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_BUY_TRIGGER", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Check if there is an existing trigger
	if existingBuyTrigger, exists := buyTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		existingBuyTrigger.BuyPrice = req.Amount

		auditEventU := UserCommand{Server: SERVER, Command: "SET_BUY_TRIGGER", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: existingBuyTrigger.BuyAmount, TransactionNum: req.TransactionNum}
		audit(auditEventU)

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
	}{"", "", 0, 0}

	err := decoder.Decode(&req)

	if err != nil {
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

	//hit audit server
	auditEventU := UserCommand{Server: SERVER, Command: "SET_SELL_AMMOUNT", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, TransactionNum: req.TransactionNum}
	audit(auditEventU)

	w.WriteHeader(http.StatusOK)
}

func cancelSetSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		StockSymbol    string
		TransactionNum int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
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

		auditEventU := UserCommand{Server: SERVER, Command: "CANCEL_SET_SELL", Username: req.UserId, StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: 0, TransactionNum: req.TransactionNum}
		audit(auditEventU)

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
	}{"", "", 0, 0}

	err := decoder.Decode(&req)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "SET_SELL_TRIGGER", StockSymbol: req.StockSymbol, Filename: FILENAME, Funds: req.Amount, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	if existingSellTrigger, exists := sellTriggerMap[req.UserId+","+req.StockSymbol]; exists {
		//	REMOVE THE MAXIMUM NUMBER OF STOCKS THAT COULD BE NEEDED TO FILL THIS SELL ORDER
		existingSellTrigger.StockSellAmount = int(math.Ceil(float64(existingSellTrigger.SellAmount) / float64(req.Amount)))

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
			failWithStatusCode(errors.New("Couldn't update portfolio"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
			return
		}

		auditEventU := UserCommand{Server: SERVER, Command: "SET_SELL_TRIGGER", Username: req.UserId, StockSymbol: existingSellTrigger.StockSymbol, Filename: FILENAME, Funds: existingSellTrigger.SellAmount, TransactionNum: req.TransactionNum}
		audit(auditEventU)

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
	}{"", 0}

	err := decoder.Decode(&req)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "DISPLAY_SUMMARY", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Bad Request", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	//	Get their current balance
	queryString := "SELECT funds FROM users WHERE user_name = $1"

	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "DISPLAY_SUMMARY", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error fetching account information", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError, auditError)
		return
	}

	var userFunds int
	err = stmt.QueryRow(req.UserId).Scan(&userFunds)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "DISPLAY_SUMMARY", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error fetching account information", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	auditEvent := UserCommand{Server: SERVER, Command: "DISPLAY_SUMMARY", Username: req.UserId, StockSymbol: "", Filename: FILENAME, Funds: userFunds, TransactionNum: req.TransactionNum}

	audit(auditEvent)

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "{\"funds\": %d}", userFunds)
}

func dumpLogHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId         string
		FileName       string
		TransactionNum int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil || req.FileName == "" {
		auditError := ErrorEvent{Server: SERVER, Command: "DUMPLOG", StockSymbol: "", Filename: FILENAME, Funds: 0, Username: req.UserId, ErrorMessage: "Error fetching account information", TransactionNum: req.TransactionNum}
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest, auditError)
		return
	}

	if req.UserId == "" {
		//	Dumplog of everything
		jsonValue, _ := json.Marshal(req)
		resp, err := http.Post("http://localhost:44417/dumpLog", "application/json", bytes.NewBuffer(jsonValue))
		failOnError(err, "Error sending request")
		defer resp.Body.Close()
		w.WriteHeader(http.StatusOK)
		return
	}

	//	Dumplog of only this users transactions
	jsonValue, _ := json.Marshal(req)
	resp, err := http.Post("http://localhost:44417/dumpLog", "application/json", bytes.NewBuffer(jsonValue))
	failOnError(err, "Error sending request")
	defer resp.Body.Close()

	w.WriteHeader(http.StatusOK)
}

func loadDB() *sql.DB {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", "localhost", 5432, "moonshot", "hodl", "moonshot")
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		failGracefully(err, "Failed to open Postgres")
	}
	err = db.Ping()
	if err != nil {
		failGracefully(err, "Failed to Ping Postgres")
	} else {
		fmt.Println("Connected to DB")
	}
	return db
}

func main() {
	rand.Seed(time.Now().Unix())
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
