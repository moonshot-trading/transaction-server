package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	resp, err := http.Post("http://localhost:44417/"+path, "application/json", bytes.NewBuffer(jsonValue))

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
