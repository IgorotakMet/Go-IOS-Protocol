package lua

import (
	"strings"
	"errors"
	"regexp"
	"bytes"
	"strconv"
)

type DocCommentParser struct {
	text string // always ends with \0, which doesn't appear elsewhere

	index int
}

func NewDocCommentParser(text string) (*DocCommentParser, error) {
	parser := new(DocCommentParser)
	if strings.Contains(text, "\\0") {
		return nil, errors.New("Text contains character \\0, parse failed")
	}
	parser.text = text + "\\0"
	return parser, nil
}

func (p *DocCommentParser) parse() (*Contract, error) {
	//0. preprocess
	//
	//1. checking exsistence of main function
	//2. detecting all functions and split them.
	//3. parse doccomment for each function.
	//4. return contract

	// 没有doc comment的代码将被忽略

	content := p.text

	re := regexp.MustCompile("--- .*\n(-- .*\n)*") //匹配全部注释代码

	hasMain := false
	var contract Contract

	var buffer bytes.Buffer

	for _, submatches := range re.FindAllStringSubmatchIndex(content, -1) {

		funcName := strings.Split(content[submatches[0]:submatches[1]], " ")[1]

		inputCountRe := regexp.MustCompile("@param_cnt (\\d+)")
		rtnCountRe := regexp.MustCompile("@return_cnt (\\d+)")

		inputCount, _ := strconv.Atoi(inputCountRe.FindStringSubmatch(content[submatches[0]:submatches[1]])[1])
		rtnCount, _ := strconv.Atoi(rtnCountRe.FindStringSubmatch(content[submatches[0]:submatches[1]])[1])
		method := NewMethod(funcName, inputCount, rtnCount)

		//匹配代码部分

		endRe := regexp.MustCompile("end")
		endPos := endRe.FindStringIndex(content[submatches[1]:])

		//code part: content[submatches[1]:][:endPos[1]
		contract.apis = make(map[string]Method)
		
		buffer.WriteString(content[submatches[1]:][:endPos[1]])
		buffer.WriteString("\n")

	}

	if !hasMain {
		return nil, errors.New("No main function!, parse failed")
	}
	contract.code=buffer.String()
	return &contract, nil

}
