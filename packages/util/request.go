package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Meta struct {
	Scheme     string // https, http
	Method     string // POST, GET, PUT, DELETE, PATCH ...
	Host       string // 域名
	Path       string // 路径URI
	ResultType string // 响应结果序列化方案
}

func DoRequest(meta *Meta, param map[string]interface{}, body interface{}, callback func(bytes []byte) error) error {
	u := &url.URL{
		Scheme: meta.Scheme,
		Host:   meta.Host,
		Path:   meta.Path,
	}

	query := u.Query()
	for key, value := range param {
		switch v := value.(type) {
		case string:
			query.Add(key, v)
		case int64:
			query.Add(key, fmt.Sprintf("%d", v))
		}
	}
	u.RawQuery = query.Encode()

	//logger.Info("request body: ", body)

	bs, err := json.Marshal(body)
	if err != nil {
		return err
	}
	reader := bytes.NewReader(bs)

	req, err := http.NewRequest(meta.Method, u.String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")

	client := new(http.Client)
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	bs, err = io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	//logger.Info("response body: ", string(bs))

	return callback(bs)
}
