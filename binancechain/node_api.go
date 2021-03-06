/*
 * Copyright 2018 The openwallet Authors
 * This file is part of the openwallet library.
 *
 * The openwallet library is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The openwallet library is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Lesser General Public License for more details.
 */

package binancechain

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/binance-chain/go-sdk/common/bech32"
	"github.com/binance-chain/go-sdk/common/types"
	"github.com/binance-chain/go-sdk/types/tx"
	"github.com/blocktree/go-owcdrivers/binancechainTransaction"
	"github.com/blocktree/openwallet/log"
	"github.com/imroc/req"
	"github.com/tendermint/go-amino"
	core_types "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tidwall/gjson"
	"math/big"
	"net/http"
)

type ClientInterface interface {
	Call(path string, request []interface{}) (*gjson.Result, error)
}

// A Client is a Bitcoin RPC client. It performs RPCs over HTTP using JSON
// request and responses. A Client must be configured with a secret token
// to authenticate with other Cores on the network.
type Client struct {
	BaseURL     string
	AccessToken string
	Debug       bool
	client      *req.Req
	//Client *req.Req
}

type Response struct {
	Code    int         `json:"code,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Message string      `json:"message,omitempty"`
	Id      string      `json:"id,omitempty"`
}

func NewClient(url string, debug bool) *Client {
	c := Client{
		BaseURL: url,
		//AccessToken: token,
		Debug: debug,
	}

	api := req.New()
	//trans, _ := api.Client().Transport.(*http.Transport)
	//trans.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	c.client = api

	return &c
}

// Call calls a remote procedure on another node, specified by the path.
func (c *Client) Call(path string, request interface{}, method string) (*gjson.Result, error) {

	if c.client == nil {
		return nil, errors.New("API url is not setup. ")
	}

	if c.Debug {
		log.Std.Debug("Start Request API...")
	}

	url := c.BaseURL + path

	r, err := c.client.Do(method, url, request)

	if c.Debug {
		log.Std.Debug("Request API Completed")
	}

	if c.Debug {
		log.Std.Debug("%+v", r)
	}

	err = c.isError(r)
	if err != nil {
		return nil, err
	}

	if err != nil {
		return nil, err
	}

	resp := gjson.ParseBytes(r.Bytes())

	return &resp, nil
}

func (b *Client) isError(resp *req.Resp) error {

	if resp == nil || resp.Response() == nil {
		return errors.New("Response is empty! ")
	}

	if resp.Response().StatusCode == http.StatusNoContent {
		return nil
	}

	if resp.Response().StatusCode != http.StatusOK {
		return fmt.Errorf("%s", resp.String())
	}

	return nil
}

// See 2 (end of page 4) http://www.ietf.org/rfc/rfc2617.txt
// "To receive authorization, the client sends the userid and password,
// separated by a single colon (":") character, within a base64
// encoded string in the credentials."
// It is not meant to be urlencoded.
func BasicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

//isError 是否报错
func isError(result *gjson.Result) error {
	var (
		err error
	)

	/*
		 //failed 返回错误
		 {
			 "result": null,
			 "error": {
				 "code": -8,
				 "message": "Block height out of range"
			 },
			 "id": "foo"
		 }
	*/

	if !result.Get("error").IsObject() {

		if !result.Get("result").Exists() {
			return errors.New("Response is empty! ")
		}

		return nil
	}

	errInfo := fmt.Sprintf("[%d]%s",
		result.Get("error.code").Int(),
		result.Get("error.message").String())
	err = errors.New(errInfo)

	return err
}

// 获取当前区块高度
func (c *Client) getBlockHeight() (uint64, error) {
	resp, err := c.Call("/status", nil, "GET")

	if err != nil {
		return 0, err
	}

	return resp.Get("result").Get("sync_info").Get("latest_block_height").Uint(), nil
}

// 通过高度获取区块哈希
func (c *Client) getBlockHash(height uint64) (string, error) {

	path := fmt.Sprintf("/block?height=%d", height)

	resp, err := c.Call(path, nil, "GET")

	if err != nil {
		return "", err
	}

	return resp.Get("result").Get("block_meta").Get("block_id").Get("hash").String(), nil
}

func (c *Client) getAccountNumberAndSequence(address string) (int64, int64, error) {

	prefix, hash, err := bech32.DecodeAndConvert(address)
	if err != nil || prefix != binancechainTransaction.Bech32Prefix {
		return 0, 0, err
	}

	path := "/abci_query?path=\"/store/acc/key\"&data=0x6163636F756E743A" + hex.EncodeToString(hash)
	r, err := c.Call(path, nil, "GET")

	if err != nil {
		return 0, 0, errors.New("Failed to get address' account number and sequence!")
	}
	var acc types.Account
	cdc := amino.NewCodec()
	core_types.RegisterAmino(cdc)
	types.RegisterWire(cdc)
	tx.RegisterCodec(cdc)

	respBytes, err := base64.StdEncoding.DecodeString(r.Get("result").Get("response").Get("value").String())
	if err != nil {
		return 0, 0, errors.New("Failed to get account number and sequence!")
	}

	err = cdc.UnmarshalBinaryBare(respBytes, &acc)
	if err != nil {
		return 0, 0, errors.New("Failed to get account number and sequence!")
	}

	return acc.GetAccountNumber(), acc.GetSequence(), nil
}

// 获取地址余额
func (c *Client) getBalance(address string, denom string) (*AddrBalance, error) {
	prefix, hash, err := bech32.DecodeAndConvert(address)
	if err != nil || prefix != binancechainTransaction.Bech32Prefix {
		return nil, err
	}

	path := "/abci_query?path=\"/store/acc/key\"&data=0x6163636F756E743A" + hex.EncodeToString(hash)
	r, err := c.Call(path, nil, "GET")

	if r.Get("result").Get("response").Get("value").String() == "" {
		return &AddrBalance{Address: address, Balance: big.NewInt(0)}, nil
	}

	if err != nil {
		return nil, errors.New("Failed to get ["+denom+"] balance of address ["+address+"]!")
	}
	var acc types.Account
	cdc := amino.NewCodec()
	core_types.RegisterAmino(cdc)
	types.RegisterWire(cdc)
	tx.RegisterCodec(cdc)

	respBytes, err := base64.StdEncoding.DecodeString(r.Get("result").Get("response").Get("value").String())
	if err != nil {
		return nil, errors.New("Failed to get ["+denom+"] balance of address ["+address+"]!")
	}

	err = cdc.UnmarshalBinaryBare(respBytes, &acc)
	if err != nil {
		return nil, errors.New("Failed to get ["+denom+"] balance of address ["+address+"]!")
	}

	coins := acc.GetCoins()

	for _, coin := range coins {
		if coin.Denom == denom {
			return &AddrBalance{Address: address, Balance: big.NewInt(coin.Amount)}, nil
		}
	}

	return &AddrBalance{Address: address, Balance: big.NewInt(0)}, nil
}

// 获取区块信息
func (c *Client) getBlock(hash string) (*Block, error) {
	return nil, nil
}

func (c *Client) getBlockByHeight(height uint64) (*Block, error) {
	path := fmt.Sprintf("/block?height=%d", height)

	resp, err := c.Call(path, nil, "GET")

	if err != nil {
		return nil, err
	}

	result := resp.Get("result")
	return NewBlock(&result), nil
}

func (c *Client) getTransaction(txid string) (*Transaction, error) {
	path := "/tx?hash=0x" +  txid

	resp, err := c.Call(path, nil, "GET")

	if err != nil {
		return nil, err
	}

	result := resp.Get("result")
	return NewTransaction(&result), nil
}

func (c *Client) getMultiFeeByHeight(height uint64) (uint64, error) {
	path := fmt.Sprintf("/abci_query?path=\"/param/fees\"&height=%d", height)
	resp, err := c.Call(path, nil, "GET")

	if err != nil {
		return 0, err
	}

	base64Decoder := base64.StdEncoding

	data, err := base64Decoder.DecodeString(resp.Get("result").Get("response").Get("value").String())
	if err != nil {
		return 0, err
	}

	cdc := amino.NewCodec()
	core_types.RegisterAmino(cdc)
	types.RegisterWire(cdc)
	tx.RegisterCodec(cdc)

	var fees []types.FeeParam

	err = cdc.UnmarshalBinaryLengthPrefixed(data, &fees)

	if err != nil {
		return 0, err
	}

	for _, fee := range fees {
		if fee.GetParamType() == "transfer" {
			return uint64(fee.(*types.TransferFeeParam).MultiTransferFee), nil
		}
	}

	return 0, errors.New("Get fee failed!")
}


func (c *Client) getFeeByHeight(height uint64) (uint64, error) {
	path := fmt.Sprintf("/abci_query?path=\"/param/fees\"&height=%d", height)
	resp, err := c.Call(path, nil, "GET")

	if err != nil {
		return 0, err
	}

	base64Decoder := base64.StdEncoding

	data, err := base64Decoder.DecodeString(resp.Get("result").Get("response").Get("value").String())
	if err != nil {
		return 0, err
	}

	cdc := amino.NewCodec()
	core_types.RegisterAmino(cdc)
	types.RegisterWire(cdc)
	tx.RegisterCodec(cdc)

	var fees []types.FeeParam

	err = cdc.UnmarshalBinaryLengthPrefixed(data, &fees)

	if err != nil {
		return 0, err
	}

	for _, fee := range fees {
		if fee.GetParamType() == "transfer" {
			return uint64(fee.(*types.TransferFeeParam).FixedFeeParams.Fee), nil
		}
	}

	return 0, errors.New("Get fee failed!")
}

func (c *Client) sendTransaction(jsonStr string) (string, error) {

	path := "/broadcast_tx_commit?tx=0x" + jsonStr

	resp, err := c.Call(path, nil, "GET")
	if err != nil {
		return "", err
	}
	if resp.Get("result").Get("height").Uint() == 0 {
		return "", errors.New("send transaction failed with error:" + resp.Get("result").Get("check_tx").String())
	}

	return resp.Get("result").Get("hash").String(), nil
}
