package main

import (
	"fmt"

	pbstarknet "github.com/streamingfast/firehose-starknet/pb/sf/starknet/type/v1"

	"github.com/NethermindEth/juno/core/felt"
	starknetRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

var marshalers = json.NewMarshalers(
	json.MarshalFuncV2(encodeTransactionWithReceipt),
	json.MarshalFuncV2(encodeRPCTransactionWithReceipt),
	json.MarshalFuncV2(encodeBytes),
	json.MarshalFuncV2(encodeDAMode),
	json.MarshalFuncV2(encodeExecutionStatus),
	json.MarshalFuncV2(encodeTransactionType),
	json.MarshalFuncV2(encodeFeeDataAvailabilityMode),
)

var jsonOptions = []json.Options{
	json.StringifyNumbers(true),
	json.RejectUnknownMembers(false),
}

func init() {
	jsonOptions = append(jsonOptions, json.WithMarshalers(marshalers))
}

func encodeTransactionType(encoder *jsontext.Encoder, t pbstarknet.TRANSACTION_TYPE, options json.Options) error {
	switch t {
	case pbstarknet.TRANSACTION_TYPE_DECLARE:
		return encoder.WriteToken(jsontext.String("DECLARE"))
	case pbstarknet.TRANSACTION_TYPE_DEPLOY:
		return encoder.WriteToken(jsontext.String("DEPLOY"))
	case pbstarknet.TRANSACTION_TYPE_INVOKE:
		return encoder.WriteToken(jsontext.String("INVOKE"))
	case pbstarknet.TRANSACTION_TYPE_L1_HANDLER:
		return encoder.WriteToken(jsontext.String("L1_HANDLER"))
	case pbstarknet.TRANSACTION_TYPE_DEPLOY_ACCOUNT:
		return encoder.WriteToken(jsontext.String("DEPLOY_ACCOUNT"))
	default:
		panic(fmt.Errorf("unknown transaction type %v", t))
	}
}
func encodeExecutionStatus(encoder *jsontext.Encoder, s pbstarknet.EXECUTION_STATUS, options json.Options) error {
	switch s {
	case pbstarknet.EXECUTION_STATUS_EXECUTION_STATUS_SUCCESS:
		return encoder.WriteToken(jsontext.String("SUCCEEDED"))
	case pbstarknet.EXECUTION_STATUS_EXECUTION_STATUS_REVERTED:
		return encoder.WriteToken(jsontext.String("REVERTED"))
	default:
		panic(fmt.Errorf("unknown execution status %v", s))
	}

}

func encodeFeeDataAvailabilityMode(encoder *jsontext.Encoder, m pbstarknet.FEE_DATA_AVAILABILITY_MODE, options json.Options) error {
	return encoder.WriteToken(jsontext.String(pbstarknet.FEE_DATA_AVAILABILITY_MODE_name[int32(m)]))
}

func encodeDAMode(encoder *jsontext.Encoder, mode pbstarknet.L1_DA_MODE, options json.Options) error {
	return encoder.WriteToken(jsontext.String(pbstarknet.L1_DA_MODE_name[int32(mode)]))
}

func encodeBytes(encoder *jsontext.Encoder, data []byte, options json.Options) error {
	f := &felt.Felt{}
	f = f.SetBytes(data)
	return encoder.WriteToken(jsontext.String(f.String()))
}

func encodeTransactionWithReceipt(encoder *jsontext.Encoder, transactionWithReceipt *pbstarknet.TransactionWithReceipt, options json.Options) error {

	tx := transactionWithReceipt.GetTransaction()
	cnt := []byte{}
	name := ""
	var err error

	switch t := tx.(type) {
	case *pbstarknet.TransactionWithReceipt_DeployTransactionV0:
		name = "deploy_transaction_v0"
		cnt, err = json.Marshal(t.DeployTransactionV0, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV1:
		name = "deploy_account_transaction_v1"
		cnt, err = json.Marshal(t.DeployAccountTransactionV1, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV3:
		name = "deploy_account_transaction_v3"
		cnt, err = json.Marshal(t.DeployAccountTransactionV3, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_DeclareTransactionV0:
		name = "declare_transaction_v0"
		cnt, err = json.Marshal(t.DeclareTransactionV0, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_DeclareTransactionV1:
		name = "declare_transaction_v1"
		cnt, err = json.Marshal(t.DeclareTransactionV1, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_DeclareTransactionV2:
		name = "declare_transaction_v2"
		cnt, err = json.Marshal(t.DeclareTransactionV2, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_DeclareTransactionV3:
		name = "declare_transaction_v3"
		cnt, err = json.Marshal(t.DeclareTransactionV3, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_InvokeTransactionV0:
		name = "invoke_transaction_v0"
		cnt, err = json.Marshal(t.InvokeTransactionV0, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_InvokeTransactionV1:
		name = "invoke_transaction_v1"
		cnt, err = json.Marshal(t.InvokeTransactionV1, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_InvokeTransactionV3:
		name = "invoke_transaction_v3"
		cnt, err = json.Marshal(t.InvokeTransactionV3, jsonOptions...)
	case *pbstarknet.TransactionWithReceipt_L1HandlerTransaction:
		name = "l1_handler_transaction"
		cnt, err = json.Marshal(t.L1HandlerTransaction, jsonOptions...)

	default:
		panic(fmt.Errorf("unknown transaction type %T", tx))
	}

	if err != nil {
		return fmt.Errorf("json marshalling tx: %w", err)
	}

	if err := encoder.WriteToken(jsontext.ObjectStart); err != nil {
		return fmt.Errorf("writing ObjectStart token: %w", err)
	}

	if err := encoder.WriteToken(jsontext.String(name)); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}
	//
	if err := encoder.WriteValue(cnt); err != nil {
		return fmt.Errorf("writing tx value: %w", err)
	}

	cnt, err = json.Marshal(
		transactionWithReceipt.Receipt,
		jsonOptions...,
	)
	if err != nil {
		return fmt.Errorf("json marshalling receipt: %w", err)
	}

	if err := encoder.WriteToken(jsontext.String("receipt")); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	if err := encoder.WriteValue(cnt); err != nil {
		return fmt.Errorf("writing receipt value: %w", err)
	}

	//
	if err := encoder.WriteToken(jsontext.ObjectEnd); err != nil {
		return fmt.Errorf("writing ObjectEnd token: %w", err)
	}

	return nil
}
func encodeRPCTransactionWithReceipt(encoder *jsontext.Encoder, transactionWithReceipt starknetRPC.TransactionWithReceipt, options json.Options) error {
	tx := transactionWithReceipt.Transaction.Transaction

	name := mustTransactionName(tx)
	cnt, err := json.Marshal(
		tx,
		jsonOptions...,
	)

	if err != nil {
		return fmt.Errorf("json marshalling tx: %w", err)
	}

	if err := encoder.WriteToken(jsontext.ObjectStart); err != nil {
		return fmt.Errorf("writing ObjectStart token: %w", err)
	}

	if err := encoder.WriteToken(jsontext.String(name)); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	if err := encoder.WriteValue(cnt); err != nil {
		return fmt.Errorf("writing tx value: %w", err)
	}

	if err := encoder.WriteToken(jsontext.String("receipt")); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	receipt := transactionWithReceipt.Receipt.TransactionReceipt
	cnt, err = json.Marshal(
		receipt,
		jsonOptions...,
	)
	if err != nil {
		return fmt.Errorf("json marshalling receipt: %w", err)
	}

	if err := encoder.WriteValue(cnt); err != nil {
		return fmt.Errorf("writing receipt value: %w", err)
	}

	if err := encoder.WriteToken(jsontext.ObjectEnd); err != nil {
		return fmt.Errorf("writing ObjectEnd token: %w", err)
	}

	return nil
}

func mustTransactionName(tx starknetRPC.Transaction) string {
	switch t := tx.(type) {
	case starknetRPC.InvokeTxnV0:
		t.Type = ""
		return "invoke_transaction_v0"
	case starknetRPC.InvokeTxnV1:
		t.Type = ""
		return "invoke_transaction_v1"
	case starknetRPC.InvokeTxnV3:
		t.Type = ""
		return "invoke_transaction_v3"
	case starknetRPC.L1HandlerTxn:
		t.Type = ""
		return "l1_handler_transaction"
	case starknetRPC.DeclareTxnV0:
		t.Type = ""
		return "declare_transaction_v0"
	case starknetRPC.DeclareTxnV1:
		t.Type = ""
		return "declare_transaction_v1"
	case starknetRPC.DeclareTxnV2:
		t.Type = ""
		return "declare_transaction_v2"
	case starknetRPC.DeclareTxnV3:
		t.Type = ""
		return "declare_transaction_v3"
	case starknetRPC.DeployTxn:
		t.Type = ""
		return "deploy_transaction_v0"
	case starknetRPC.DeployAccountTxn:
		t.Type = ""
		return "deploy_account_transaction_v1"
	case starknetRPC.DeployAccountTxnV3:
		t.Type = ""
		return "deploy_account_transaction_v3"
	default:
		panic(fmt.Errorf("unknown transaction type %T", tx))
	}
}
