package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/garyburd/redigo/redis"
)

func runningInDocker() bool {
	_, err := os.Stat("/.dockerenv")
	if err == nil {
		return true
	}
	return false
}

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

func audit(auditStruct interface{}) {
	// var path string
	// //  Check the type of auditStruct
	switch auditStruct.(type) {
	case AccountTransaction:
		transactionChannel <- auditStruct

	// case SystemEvent:
	// 	path = "systemEvent"

	case ErrorEvent:
		errorChannel <- auditStruct

	// case DebugEvent:
	// 	path = "debugEvent"

	case QuoteServer:
		quoteChannel <- auditStruct

	case UserCommand:
		userChannel <- auditStruct
	}
}

func clearBuys() {
	for {
		time.Sleep(25000 * time.Millisecond)

		buyMap.Range(func(key, element interface{}) bool {
			topBuy := element.(Stacker).Peek()

			if topBuy != nil {
				buyTime := topBuy.(Buy).BuyTimestamp
				currentTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

				//	if top one is too old, then the whole stack needs to be deleted
				if buyTime+60000 < currentTime {

					for element.(Stacker).Peek() != nil {
						// cancel them repeatedly
						nextBuy := element.(Stacker).Pop()
						replaceFunds(nextBuy.(Buy), key.(string))
					}
				}
			}
			return true
		})
	}
}

func clearSells() {
	for {
		time.Sleep(25000 * time.Millisecond)

		sellMap.Range(func(key, element interface{}) bool {
			topSell := element.(Stacker).Peek()

			if topSell != nil {
				sellTime := topSell.(Sell).SellTimestamp
				currentTime := int64(time.Nanosecond) * int64(time.Now().UnixNano()) / int64(time.Millisecond)

				if sellTime+60000 < currentTime {
					for element.(Stacker).Peek() != nil {
						//nextSell := sellMap[userID].Pop()
						nextSell := element.(Stacker).Pop()
						replaceStocks(nextSell.(Sell), key.(string))
					}
				}
			}
			return true
		})
	}
}

func replaceFunds(thisBuy Buy, userID string) {
	c := Pool.Get()
	defer c.Close()

	if c == nil {
		fmt.Println("lol no db haha")
	}
	_, rediserr := c.Do("INCRBY", userID, thisBuy.BuyAmount)

	if rediserr != nil {
		auditError := ErrorEvent{Server: SERVER, Command: "CANCEL_BUY", StockSymbol: thisBuy.StockSymbol, Filename: FILENAME, Funds: thisBuy.BuyAmount, Username: userID, ErrorMessage: "Error replacing funds", TransactionNum: 5}
		audit(auditError)
		failGracefully(rediserr, "***COULD NOT REPLACE FUNDS")
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

// Can take negative fundsAmount for removing funds from account.
func writeFundsThroughCache(userId string, fundsAmount int) error {
	//	Get current val from redis, if it will go negative, return before running more queries
	c := Pool.Get()
	defer c.Close()

	if c == nil {
		return errors.New("Error connecting to redis")
	}

	res, rediserr := redis.Int(c.Do("GET", userId))

	//	If this error is set, then we didnt recieve anything for the key userId
	//	Need to add row for this user to pg, and entry in redis
	if rediserr != nil {
		//	check if trying to remove funds from a non existant account
		if fundsAmount < 0 {
			return errors.New("can't remove funds from non-existant account")
		}
		queryString := "INSERT INTO users(user_name, amount) VALUES($1, $2)"
		stmt, err := db.Prepare(queryString)
		if err != nil {
			return err
		}
		_, err = stmt.Exec(userId, fundsAmount)
		if err != nil {
			return err
		}
		_, rediserr = c.Do("SET", userId, fundsAmount)
		if rediserr != nil {
			return err
		}
		return nil
	}

	//	if we get here, we are incrementing/decrementing an existing balance
	if res+fundsAmount < 0 {
		return errors.New("account operation would put balance negative")
	}

	//	Write to the redis cache
	_, rediserr = c.Do("SET", userId, fundsAmount+res)

	if rediserr != nil {
		return rediserr
	}

	//	Write to pg
	queryString := "UPDATE users SET funds = users.funds + $1 WHERE user_name = $2"
	stmt, err := db.Prepare(queryString)
	if err != nil {
		fmt.Println("Error preparing")
		return err
	}
	pgres, err := stmt.Exec(userId, fundsAmount)

	if err != nil {
		return err
	}

	numRows, err := pgres.RowsAffected()

	if numRows < 1 {
		return errors.New("error writing funds to postgres")
	}

	return nil
}

// Can take negative stockAmount for removing stocks from account.
func writeStocksThroughCache(userId string, stockSymbol string, stockAmount int) error {
	//	Get current val from redis, if it will go negative, return before running more queries
	c := Pool.Get()
	defer c.Close()

	if c == nil {
		return errors.New("Error connecting to redis")
	}
	res, rediserr := redis.Int(c.Do("GET", userId+","+stockSymbol))

	if rediserr != nil {
		if stockAmount < 0 {
			return errors.New("can't remove stocks from non existing account")
		}

		queryString := "INSERT INTO stocks(user_name, , stock_symbol, amount) VALUES($1, $2, $3)"
		stmt, err := db.Prepare(queryString)
		if err != nil {
			return err
		}
		_, err = stmt.Exec(userId, stockSymbol, stockAmount)
		if err != nil {
			return err
		}
		_, rediserr = c.Do("SET", userId+","+stockSymbol, res+stockAmount)
		if rediserr != nil {
			return err
		}
		return nil
	}

	//	if we get to here then we need to check if the increment/decrement is going to be ok
	if res+stockAmount < 0 {
		return errors.New("account operation would put stock amount negative")
	}

	//	write to redis
	_, rediserr = c.Do("SET", userId+","+stockSymbol, res+stockAmount)

	if rediserr != nil {
		return rediserr
	}

	//	Write to pg
	queryString := "UPDATE stocks SET amount = stocks.amount - $1 WHERE user_name = $2 AND stock_symbol = $3"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		return err
	}

	pgres, err := stmt.Exec(stockAmount, userId, stockSymbol)

	if err != nil {
		return err
	}

	numRows, err := pgres.RowsAffected()

	if numRows < 1 {
		return errors.New("error writing stocks to postgres")
	}

	return nil
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
