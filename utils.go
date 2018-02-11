package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func failOnError(err error, msg string) {
	if err != nil {
		fmt.Printf("%s: %s", msg, err)
		panic(err)
	}
}

func failWithStatusCode(err error, msg string, w http.ResponseWriter, statusCode int, auditError ErrorEvent) {
	failGracefully(err, msg)
	audit(auditError)
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, msg)
}

func failGracefully(err error, msg string) {
	if err != nil {
		fmt.Printf("%s: %s", msg, err)
	}
}

func sendToAuditServer(auditStruct interface{}, path string) {
	jsonValue, _ := json.Marshal(auditStruct)
	resp, err := http.Post("http://audit-server:44417/"+path, "application/json", bytes.NewBuffer(jsonValue))

	if err != nil {
		fmt.Printf("***FAILED TO AUDIT: %s", err)
	}

	defer resp.Body.Close()
}

func audit(auditStruct interface{}) {
	var path string
	//  Check the type of auditStruct
	switch auditStruct.(type) {
	case AccountTransaction:
		path = "accountTransaction"

	case SystemEvent:
		path = "systemEvent"

	case ErrorEvent:
		path = "errorEvent"

	case DebugEvent:
		path = "debugEvent"

	case QuoteServer:
		path = "quoteServer"

	case UserCommand:
		path = "userCommand"
	}

	sendToAuditServer(auditStruct, path)
}

func clearBuys() {
	for {
		time.Sleep(1000 * time.Millisecond)

		for userID := range buyMap {
			topBuy := buyMap[userID].Peek()

			if topBuy != nil {
				buyTime := topBuy.(Buy).BuyTimestamp
				currentTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

				//	if top one is too old, then the whole stack needs to be deleted
				if buyTime+60000 < currentTime {

					for buyMap[userID].Peek() != nil {
						// cancel them repeatedly
						nextBuy := buyMap[userID].Pop()
						replaceFunds(nextBuy.(Buy), userID)
					}
				}
			} else {
				continue
			}
		}
	}
}

func clearSells() {
	for {
		time.Sleep(1000 * time.Millisecond)

		for userID := range sellMap {
			topSell := sellMap[userID].Peek()

			if topSell != nil {
				sellTime := topSell.(Sell).SellTimestamp
				currentTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

				if sellTime+60000 < currentTime {
					for sellMap[userID].Peek() != nil {
						nextSell := sellMap[userID].Pop()
						replaceStocks(nextSell.(Sell), userID)
					}
				}
			} else {
				continue
			}
		}

	}
}

func replaceFunds(thisBuy Buy, userID string) {
	queryString := "UPDATE users SET funds = funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: thisBuy.StockSymbol, Filename: FILENAME, Funds: thisBuy.BuyAmount, Username: userID, ErrorMessage: "Error replacing funds", TransactionNum: 5}
		audit(auditError)
		failGracefully(err, "***COULD NOT REPLACE FUNDS")
		return
	}

	_, err = stmt.Exec(thisBuy.BuyAmount, userID)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: thisBuy.StockSymbol, Filename: FILENAME, Funds: thisBuy.BuyAmount, Username: userID, ErrorMessage: "Error replacing funds", TransactionNum: 5}
		audit(auditError)
		failGracefully(err, "***COULD NOT REPLACE FUNDS")
		return
	}
}

func replaceStocks(thisSell Sell, userID string) {
	queryString := "UPDATE stocks SET amount = amount + $1 WHERE user_name = $2 AND stock_symbol = $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: thisSell.StockSymbol, Filename: FILENAME, Funds: thisSell.SellAmount, Username: userID, ErrorMessage: "Error replacing stocks", TransactionNum: 7}
		audit(auditError)
		failGracefully(err, "***COULD NOT REPLACE STOCKS")
		return
	}

	_, err = stmt.Exec(thisSell.StockSellAmount, userID, thisSell.StockSymbol)

	if err != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_SELL", StockSymbol: thisSell.StockSymbol, Filename: FILENAME, Funds: thisSell.SellAmount, Username: userID, ErrorMessage: "Error replacing stocks", TransactionNum: 7}
		audit(auditError)
		failGracefully(err, "***COULD NOT REPLACE STOCKS")
		return
	}
}

//  Stack implementation
type Stacker interface {
	Len() int
	Push(interface{})
	Pop() interface{}
	Peek() interface{}
}

type Stack struct {
	topPtr *stackElement
	size   int
}

type stackElement struct {
	value interface{}
	next  *stackElement
}

func (s Stack) Len() int {
	return s.size
}

func (s *Stack) Push(v interface{}) {
	s.topPtr = &stackElement{
		value: v,
		next:  s.topPtr,
	}
	s.size++
}

func (s *Stack) Pop() interface{} {
	if s.size > 0 {
		retVal := s.topPtr.value
		s.topPtr = s.topPtr.next
		s.size--
		return retVal
	}
	return nil
}

func (s Stack) Peek() interface{} {
	if s.size > 0 {
		return s.topPtr.value
	}
	return nil
}

func floatStringToCents(val string) int {
	cents, _ := strconv.Atoi(strings.Replace(val, ".", "", 1))
	return cents
}
