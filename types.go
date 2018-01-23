package main

type Buy struct {
	BuyTimestamp	int
	QuoteTimestamp	int
	QuoteCryptoKey	string
	StockSymbol		string
	StockPrice		float64
	BuyAmount		int
}

type Sell struct {
	SellTimestamp	int
	QuoteTimestamp	int
	QuoteCryptoKey	string
	StockSymbol		string
	StockPrice		float64
	SellAmount		int
	StockSellAmount	int
}

//	Auditing types
type AccountTransaction struct {
	Server		string		`json:"server"`
	Action		string		`json:"action"`
	Username	string		`json:"username"`
	Funds		int			`json:"funds"`
}

type SystemEvent struct {
	Server		string		`json:"server"`
	Command		string		`json:"command"`
	StockSymbol	string		`json:"stockSymbol"`
	Username	string		`json:"username"`
	Filename	string		`json:"filename"`	
	Funds		int			`json:"funds"`
}

type ErrorEvent struct {
	Server			string		`json:"server"`
	Command			string		`json:"command"`
	StockSymbol		string		`json:"stockSymbol"`
	Filename		string		`json:"filename"`	
	Funds			int			`json:"funds"`
	Username		string		`json:"username"`
	ErrorMessage	string		`json:"errorMessage"`
}

type DebugEvent struct {
	Server			string		`json:"server"`
	Command			string		`json:"command"`
	StockSymbol		string		`json:"stockSymbol"`
	Filename		string		`json:"filename"`	
	Funds			int			`json:"funds"`
	Username		string		`json:"username"`
	DebugMessage	string		`json:"debugMessage"`	
}

type QuoteServer struct {
	Server			string		`json:"server"`
	Price			int			`json:"price"`
	StockSymbol		string		`json:"stockSymbol"`
	Username		string		`json:"username"`
	QuoteServerTime	int			`json:"quoteServerTime"`	
	Cryptokey		string		`json:"cryptokey"`
}

type UserCommand struct {
	Server			string		`json:"server"`
	Command			string		`json:"command"`
	Username		string		`json:"username"`
	StockSymbol		string		`json:"stockSymbol"`
	Filename		string		`json:"filename"`	
	Funds			int			`json:"funds"`	
}