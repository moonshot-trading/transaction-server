package main

import (
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
	quoteServerURL   = "localhost"
	quoteServerPort  = 44415
	db               = loadDB()
	buyMap           = make(map[string]*Stack)
	quoteMap		 = make(map[string]Quote)
	sellMap          = make(map[string]*Stack)
	setBuy           = false
	setBuyValue      = 0
	triggerBuy       = false
	triggerBuyValue  = 0
	setSell          = false
	setSellValue     = 0
	triggerSell      = false
	triggerSellValue = 0
)

type Quote struct {
	Price       string
	StockSymbol string
	UserId      string
	Timestamp   int
	CryptoKey   string
}

func getQuote(commandString string, UserId string) (string, error) {
	//	Check if the user exists, and if they have the necessary balance to request a quote
	stockSymbol := strings.Split(commandString, ",")[1]
	
	quoteTime := int(time.Now().Unix())

	if cachedQuote, exists := quoteMap[stockSymbol]; exists {
		if cachedQuote.Timestamp + 60 > quoteTime {
			quoteString := cachedQuote.Price + "," + cachedQuote.StockSymbol + "," + cachedQuote.UserId + "," + strconv.Itoa(cachedQuote.Timestamp) + "," + cachedQuote.CryptoKey
			return quoteString, nil
		}
	}

	conn, err := net.Dial("tcp", "localhost:44415")
	defer conn.Close()

	if err != nil {
		fmt.Println("Connection error")
		return "", err
	}

	conn.Write([]byte(commandString + "\n"))
	buff := make([]byte, 1024)
	length, _ := conn.Read(buff)

	quoteString := string(buff[:length])

	quoteStringComponents := strings.Split(quoteString, ",")
    thisQuote := Quote{}

    thisQuote.Price = quoteStringComponents[0]
    thisQuote.StockSymbol = quoteStringComponents[1]
    thisQuote.UserId = UserId
    thisQuote.Timestamp, _ = strconv.Atoi(quoteStringComponents[3])
	thisQuote.CryptoKey = quoteStringComponents[4]
	
	quoteMap[stockSymbol] = thisQuote

	return quoteString, nil
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Transaction server connection successful")
}

func quoteHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
	}{"", ""}

	err := decoder.Decode(&req)

	if err != nil || len(req.StockSymbol) > 3 {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	commandString := req.UserId + "," + req.StockSymbol
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	quoteStringComponents := strings.Split(quoteString, ",")

	quote := Quote{}
	quote.Price = quoteStringComponents[0]
	quote.StockSymbol = quoteStringComponents[1]
	quote.UserId = quoteStringComponents[2]
	quote.Timestamp, _ = strconv.Atoi(quoteStringComponents[3])
	quote.CryptoKey = quoteStringComponents[4]

	quoteJson, err := json.Marshal(quote)
	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, string(quoteJson))
}

func addHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId string
		Amount int
	}{"", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	queryString := "INSERT INTO users(user_name, funds) VALUES($1, $2) ON CONFLICT (user_name) DO UPDATE SET funds = users.funds + $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	res, err := stmt.Exec(req.UserId, req.Amount)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func buyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
		Amount      int
	}{"", "", 0}

	err := decoder.Decode(&req)
	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	buyTime := int(time.Now().Unix())

	//	Check if user has funds to buy at this price
	queryString := "UPDATE users SET funds = users.funds - $1 WHERE user_name = $2"

	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	res, err := stmt.Exec(req.Amount, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	//	Get a quote
	commandString := req.UserId + "," + req.StockSymbol
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	// Parse Quote
	quoteStringComponents := strings.Split(quoteString, ",")
	thisBuy := Buy{}

	thisBuy.BuyTimestamp = buyTime
	thisBuy.QuoteTimestamp, _ = strconv.Atoi(quoteStringComponents[3])
	thisBuy.QuoteCryptoKey = quoteStringComponents[4]
	thisBuy.StockSymbol = quoteStringComponents[1]
	thisBuy.StockPrice, _ = strconv.ParseFloat(quoteStringComponents[0], 64)
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
		UserId string
	}{""}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if buyMap[req.UserId] == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	latestBuy := buyMap[req.UserId].Pop()

	if latestBuy == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	//	Return funds to account
	queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	_, err = stmt.Exec(latestBuy.(Buy).BuyAmount, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func confirmBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId string
	}{""}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if buyMap[req.UserId] == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	latestBuy := buyMap[req.UserId].Pop()

	if latestBuy == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
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
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	_, err = stmt.Exec(refundAmount, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	//	Give stocks to user
	queryString = "INSERT INTO stocks(user_name, stock_symbol, amount) VALUES($1, $2, $3) ON CONFLICT (user_name, stock_symbol) DO UPDATE SET amount = stocks.amount + $3"
	stmt, err = db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	fmt.Println("commit buy", req, latestBuy, stockQuantity)
	_, err = stmt.Exec(req.UserId, latestBuy.(Buy).StockSymbol, stockQuantity)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	//	Return resp to client
	w.WriteHeader(http.StatusOK)
}

func sellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
		Amount      int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	sellTime := int(time.Now().Unix())

	//	Get a quote
	commandString := req.UserId + "," + req.StockSymbol
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	//	Parse quote
	quoteStringComponents := strings.Split(quoteString, ",")
	thisSell := Sell{}

	thisSell.SellTimestamp = sellTime
	thisSell.QuoteTimestamp, _ = strconv.Atoi(quoteStringComponents[3])
	thisSell.QuoteCryptoKey = quoteStringComponents[4]
	thisSell.StockSymbol = quoteStringComponents[1]
	thisSell.StockPrice, _ = strconv.ParseFloat(quoteStringComponents[0], 64)
	thisSell.SellAmount = req.Amount
	thisSell.StockSellAmount = int(math.Ceil(float64(req.Amount) / (thisSell.StockPrice * 100)))

	//	Check if they have enough stock to sell at this price
	queryString := "UPDATE stocks SET amount = stocks.amount - $1 WHERE user_name = $2 AND stock_symbol = $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	res, err := stmt.Exec(thisSell.StockSellAmount, req.UserId, thisSell.StockSymbol)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		failWithStatusCode(errors.New("Couldn't update portfolio"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
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
		UserId string
	}{""}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if sellMap[req.UserId] == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	latestSell := sellMap[req.UserId].Pop()

	if latestSell == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	//	Return stocks to portfolio
	queryString := "UPDATE stocks SET amount = amount + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	_, err = stmt.Exec(latestSell.(Sell).StockSellAmount, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func confirmSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId string
	}{""}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if sellMap[req.UserId] == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	latestSell := sellMap[req.UserId].Pop()

	if latestSell == nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	//	Add funds to their account
	sellFunds := latestSell.(Sell).StockSellAmount * int(latestSell.(Sell).StockPrice*100)

	queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	_, err = stmt.Exec(sellFunds, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func setBuyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
		Amount      int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if setBuy == true {
		//	Cancel the old request
		queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		_, err = stmt.Exec(setBuyValue, req.UserId)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}
	}

	setBuy = false
	setBuyValue = 0

	//	Check if user has funds to buy at this price
	queryString := "UPDATE users SET funds = users.funds - $1 WHERE user_name = $2"

	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	res, err := stmt.Exec(req.Amount, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		failWithStatusCode(errors.New("Couldn't update account"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	setBuy = true
	setBuyValue = req.Amount
	//hit audit server

	//	Send response back to client
	w.WriteHeader(http.StatusOK)

}

func cancelSetBuyHandler(w http.ResponseWriter, r *http.Request) {

	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
	}{"", ""}

	err := decoder.Decode(&req)

	if err != nil {
		fmt.Println("yeet")
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if setBuy == false {
		//cancelling when no trigger has been set
		fmt.Println("cancel without set")
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	//	Return funds to account
	queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	_, err = stmt.Exec(setBuyValue, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	setBuy = false
	setBuyValue = 0

	//remove trigger also if it exists
	triggerBuy = false
	triggerBuyValue = 0
	//hit audit server

	w.WriteHeader(http.StatusOK)
}

func setBuyTriggerHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
		Amount      int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if setBuy == false {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	triggerBuy = true
	triggerBuyValue = req.Amount
	//hit audit server

	//	Get a quote
	commandString := req.UserId + "," + req.StockSymbol
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	// Parse Quote
	quoteStringComponents := strings.Split(quoteString, ",")
	thisBuy := Buy{}

	//thisBuy.BuyTimestamp = buyTime
	thisBuy.QuoteTimestamp, _ = strconv.Atoi(quoteStringComponents[3])
	thisBuy.QuoteCryptoKey = quoteStringComponents[4]
	thisBuy.StockSymbol = quoteStringComponents[1]
	thisBuy.StockPrice, _ = strconv.ParseFloat(quoteStringComponents[0], 64)
	thisBuy.BuyAmount = req.Amount

	fmt.Println(thisBuy.StockPrice * 100)
	fmt.Println(req.Amount)

	if int(thisBuy.StockPrice*100) <= req.Amount {

		//we check the current value to see if the trigger goes right away
		// this is only for milestone 1

		//do confirm buy stuff

		//	Calculate actual cost of buy
		stockQuantity := int(setBuyValue / int(thisBuy.StockPrice*100))
		actualCharge := int(thisBuy.StockPrice*100) * stockQuantity
		refundAmount := setBuyValue - actualCharge

		//	Put excess money back into account
		queryString := "UPDATE users SET funds = users.funds + $1 WHERE user_name = $2"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		_, err = stmt.Exec(refundAmount, req.UserId)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		//	Give stocks to user
		queryString = "INSERT INTO stocks(user_name, stock_symbol, amount) VALUES($1, $2, $3) ON CONFLICT (user_name, stock_symbol) DO UPDATE SET amount = stocks.amount + $3"
		stmt, err = db.Prepare(queryString)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		_, err = stmt.Exec(req.UserId, thisBuy.StockSymbol, stockQuantity)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		//I assume the trigger goes away if you fufill it
		setBuy = false
		setBuyValue = 0
		triggerBuy = false
		triggerBuyValue = 0
	}

	//	Send response back to client
	w.WriteHeader(http.StatusOK)

}

func setSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
		Amount      int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if setSell == true {
		//	Cancel old trigger
		queryString := "UPDATE stocks SET amount = amount + $1 WHERE user_name = $2 AND stock_symbol = $3"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		_, err = stmt.Exec(setSellValue, req.UserId, req.StockSymbol)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

	}

	setSell = false
	setSellValue = 0

	sellTime := int(time.Now().Unix())

	//	Get a quote
	commandString := req.UserId + "," + req.StockSymbol
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	//	Parse quote
	quoteStringComponents := strings.Split(quoteString, ",")
	thisSell := Sell{}

	thisSell.SellTimestamp = sellTime
	thisSell.QuoteTimestamp, _ = strconv.Atoi(quoteStringComponents[3])
	thisSell.QuoteCryptoKey = quoteStringComponents[4]
	thisSell.StockSymbol = quoteStringComponents[1]
	thisSell.StockPrice, _ = strconv.ParseFloat(quoteStringComponents[0], 64)
	thisSell.SellAmount = req.Amount
	thisSell.StockSellAmount = int(math.Ceil(float64(req.Amount) / (thisSell.StockPrice * 100)))

	fmt.Println(thisSell.StockPrice)
	//	Check if they have enough stock to sell at this price
	queryString := "UPDATE stocks SET amount = stocks.amount - $1 WHERE user_name = $2 AND stock_symbol = $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	res, err := stmt.Exec(thisSell.StockSellAmount, req.UserId, thisSell.StockSymbol)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		failWithStatusCode(errors.New("Couldn't update portfolio"), http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	setSell = true
	setSellValue = thisSell.StockSellAmount
	//hit audit server

	w.WriteHeader(http.StatusOK)

}

func cancelSetSellHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
	}{"", ""}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if setSell == false {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	//	Return stocks to portfolio
	queryString := "UPDATE stocks SET amount = amount + $1 WHERE user_name = $2  AND stock_symbol = $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	_, err = stmt.Exec(setSellValue, req.UserId, req.StockSymbol)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	setSell = false
	setSellValue = 0

	w.WriteHeader(http.StatusOK)
}

func setSellTriggerHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId      string
		StockSymbol string
		Amount      int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	if setSell == false {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}
	triggerSell = true
	triggerSellValue = req.Amount

	//	Get a quote
	commandString := req.UserId + "," + req.StockSymbol
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	// Parse Quote
	quoteStringComponents := strings.Split(quoteString, ",")
	thisBuy := Buy{}

	//thisBuy.BuyTimestamp = buyTime
	thisBuy.QuoteTimestamp, _ = strconv.Atoi(quoteStringComponents[3])
	thisBuy.QuoteCryptoKey = quoteStringComponents[4]
	thisBuy.StockSymbol = quoteStringComponents[1]
	thisBuy.StockPrice, _ = strconv.ParseFloat(quoteStringComponents[0], 64)
	thisBuy.BuyAmount = req.Amount

	fmt.Println(thisBuy.StockPrice * 100)
	fmt.Println(req.Amount)

	if int(thisBuy.StockPrice*100) >= req.Amount {

		//	Add funds to their account
		sellFunds := setSellValue * int(thisBuy.StockPrice*100)

		queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
		stmt, err := db.Prepare(queryString)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		_, err = stmt.Exec(sellFunds, req.UserId)

		if err != nil {
			failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
			return
		}

		//if trigger goes, turn it off
		triggerSell = false
		triggerSellValue = 0
		setSell = false
		setSellValue = 0
	}

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
	http.ListenAndServe(port, nil)
}
