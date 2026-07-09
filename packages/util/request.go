package util

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	return DoRequestWithContext(context.Background(), http.DefaultClient, meta, param, body, callback)
}

func DoRequestWithContext(ctx context.Context, client *http.Client, meta *Meta, param map[string]interface{}, body interface{}, callback func(bytes []byte) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = http.DefaultClient
	}
	if meta == nil {
		return errors.New("request meta is nil")
	}
	if callback == nil {
		return errors.New("request callback is nil")
	}

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
		case fmt.Stringer:
			query.Add(key, v.String())
		case nil:
			continue
		default:
			query.Add(key, fmt.Sprint(v))
		}
	}
	u.RawQuery = query.Encode()

	//logger.Info("request body: ", body)

	var reader io.Reader = http.NoBody
	if body != nil {
		bs, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(bs)
	}

	req, err := http.NewRequestWithContext(ctx, meta.Method, u.String(), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}

	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	bs, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("request %s %s returned status %d: %s", meta.Method, u.String(), response.StatusCode, string(bs))
	}

	//logger.Info("response body: ", string(bs))

	return callback(bs)
}
