// Copyright (c) 2020 Bojan Zivanovic and contributors
// SPDX-License-Identifier: MIT

package currency

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

// RoundingMode determines how the amount will be rounded.
type RoundingMode uint8

const (
	// RoundHalfUp rounds up if the next digit is >= 5.
	RoundHalfUp RoundingMode = iota
	// RoundHalfDown rounds up if the next digit is > 5.
	RoundHalfDown
	// RoundUp rounds away from 0.
	RoundUp
	// RoundDown rounds towards 0, truncating extra digits.
	RoundDown
)

// InvalidNumberError is returned when a numeric string can't be converted to a decimal.
type InvalidNumberError struct {
	Number string
}

func (e InvalidNumberError) Error() string {
	return fmt.Sprintf("invalid number %q", e.Number)
}

// InvalidCurrencyCodeError is returned when a currency code is invalid or unrecognized.
type InvalidCurrencyCodeError struct {
	CurrencyCode string
}

func (e InvalidCurrencyCodeError) Error() string {
	return fmt.Sprintf("invalid currency code %q", e.CurrencyCode)
}

// MismatchError is returned when two amounts have mismatched currency codes.
type MismatchError struct {
	A Amount
	B Amount
}

func (e MismatchError) Error() string {
	return fmt.Sprintf("amounts %q and %q have mismatched currency codes", e.A, e.B)
}

// Amount stores a decimal number with its currency code.
type Amount struct {
	number       apd.Decimal
	currencyCode string
}

// NewAmount creates a new Amount from a numeric string and a currency code.
func NewAmount(n, currencyCode string) (Amount, error) {
	var a Amount
	_, _, err := a.number.SetString(n)
	if err != nil {
		return Amount{}, InvalidNumberError{n}
	}
	if currencyCode == "" || !IsValid(currencyCode) {
		return Amount{}, InvalidCurrencyCodeError{currencyCode}
	}
	a.currencyCode = currencyCode
	return a, nil
}

// NewAmountFromBigInt creates a new Amount from a big.Int and a currency code.
func NewAmountFromBigInt(n *big.Int, currencyCode string) (Amount, error) {
	if n == nil {
		return Amount{}, InvalidNumberError{"nil"}
	}
	var a Amount
	d, ok := GetDigits(currencyCode)
	if !ok {
		return Amount{}, InvalidCurrencyCodeError{currencyCode}
	}
	var coeff apd.BigInt
	coeff.SetMathBigInt(n)
	a.number = *apd.NewWithBigInt(&coeff, -int32(d))
	a.currencyCode = currencyCode
	return a, nil
}

// NewAmountFromInt64 creates a new Amount from an int64 and a currency code.
func NewAmountFromInt64(n int64, currencyCode string) (Amount, error) {
	var a Amount
	d, ok := GetDigits(currencyCode)
	if !ok {
		return Amount{}, InvalidCurrencyCodeError{currencyCode}
	}
	a.number.SetFinite(n, -int32(d))
	a.currencyCode = currencyCode
	return a, nil
}

// Number returns the number as a numeric string.
func (a Amount) Number() string {
	return a.number.String()
}

// CurrencyCode returns the currency code.
func (a Amount) CurrencyCode() string {
	return a.currencyCode
}

// String returns the string representation of a.
func (a Amount) String() string {
	return a.Number() + " " + a.CurrencyCode()
}

// BigInt returns a in minor units, as a big.Int.
func (a Amount) BigInt() *big.Int {
	r := a.Round()
	return r.number.Coeff.MathBigInt()
}

// Int64 returns a in minor units, as an int64.
// If a cannot be represented in an int64, an error is returned.
func (a Amount) Int64() (int64, error) {
	n := a.Round().number
	n.Exponent = 0
	return n.Int64()
}

// Convert converts a to a different currency.
func (a Amount) Convert(currencyCode, rate string) (Amount, error) {
	if currencyCode == "" || !IsValid(currencyCode) {
		return Amount{}, InvalidCurrencyCodeError{currencyCode}
	}
	var res Amount
	_, _, err := res.number.SetString(rate)
	if err != nil {
		return Amount{}, InvalidNumberError{rate}
	}
	ctx := decimalContext(&a.number, &res.number)
	ctx.Mul(&res.number, &a.number, &res.number)
	res.currencyCode = currencyCode

	return res, nil
}

// Add adds a and b together and returns the result.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.currencyCode != b.currencyCode {
		return Amount{}, MismatchError{a, b}
	}
	var res Amount
	ctx := decimalContext(&a.number, &b.number)
	ctx.Add(&res.number, &a.number, &b.number)
	res.currencyCode = a.currencyCode

	return res, nil
}

// Sub subtracts b from a and returns the result.
func (a Amount) Sub(b Amount) (Amount, error) {
	if a.currencyCode != b.currencyCode {
		return Amount{}, MismatchError{a, b}
	}
	var res Amount
	ctx := decimalContext(&a.number, &b.number)
	ctx.Sub(&res.number, &a.number, &b.number)
	res.currencyCode = a.currencyCode

	return res, nil
}

// Mul multiplies a by n and returns the result.
func (a Amount) Mul(n string) (Amount, error) {
	var res Amount
	_, _, err := res.number.SetString(n)
	if err != nil {
		return Amount{}, InvalidNumberError{n}
	}
	ctx := decimalContext(&a.number, &res.number)
	ctx.Mul(&res.number, &a.number, &res.number)
	res.currencyCode = a.currencyCode

	return res, err
}

// Div divides a by n and returns the result.
func (a Amount) Div(n string) (Amount, error) {
	var res Amount
	_, _, err := res.number.SetString(n)
	if err != nil || res.number.IsZero() {
		return Amount{}, InvalidNumberError{n}
	}
	ctx := decimalContext(&a.number, &res.number)
	ctx.Quo(&res.number, &a.number, &res.number)
	res.currencyCode = a.currencyCode

	return res, err
}

// Round is a shortcut for RoundTo(currency.DefaultDigits, currency.RoundHalfUp).
func (a Amount) Round() Amount {
	return a.RoundTo(DefaultDigits, RoundHalfUp)
}

// RoundTo rounds a to the given number of fraction digits.
func (a Amount) RoundTo(digits uint8, mode RoundingMode) Amount {
	if digits == DefaultDigits {
		digits, _ = GetDigits(a.currencyCode)
	}
	extModes := map[RoundingMode]apd.Rounder{
		RoundHalfUp:   apd.RoundHalfUp,
		RoundHalfDown: apd.RoundHalfDown,
		RoundUp:       apd.RoundUp,
		RoundDown:     apd.RoundDown,
	}
	var res Amount
	ctx := decimalContext(&a.number)
	ctx.Rounding = extModes[mode]
	ctx.Quantize(&res.number, &a.number, -int32(digits))
	res.currencyCode = a.currencyCode

	return res
}

// Cmp compares a and b and returns:
//
//   -1 if a <  b
//    0 if a == b
//   +1 if a >  b
//
func (a Amount) Cmp(b Amount) (int, error) {
	if a.currencyCode != b.currencyCode {
		return -1, MismatchError{a, b}
	}
	return a.number.Cmp(&b.number), nil
}

// Equal returns whether a and b are equal.
func (a Amount) Equal(b Amount) bool {
	if a.currencyCode != b.currencyCode {
		return false
	}
	return a.number.Cmp(&b.number) == 0
}

// IsPositive returns whether a is positive.
func (a Amount) IsPositive() bool {
	zero := apd.New(0, 0)
	return a.number.Cmp(zero) == 1
}

// IsNegative returns whether a is negative.
func (a Amount) IsNegative() bool {
	zero := apd.New(0, 0)
	return a.number.Cmp(zero) == -1
}

// IsZero returns whether a is zero.
func (a Amount) IsZero() bool {
	zero := apd.New(0, 0)
	return a.number.Cmp(zero) == 0
}

// MarshalBinary implements the encoding.BinaryMarshaler interface.
func (a Amount) MarshalBinary() ([]byte, error) {
	buf := bytes.Buffer{}
	buf.WriteString(a.CurrencyCode())
	buf.WriteString(a.Number())

	return buf.Bytes(), nil
}

// UnmarshalBinary implements the encoding.BinaryUnmarshaler interface.
func (a *Amount) UnmarshalBinary(data []byte) error {
	if len(data) < 3 {
		return InvalidCurrencyCodeError{string(data)}
	}
	n := string(data[3:])
	currencyCode := string(data[0:3])
	_, _, err := a.number.SetString(n)
	if err != nil {
		return InvalidNumberError{n}
	}
	if currencyCode == "" || !IsValid(currencyCode) {
		return InvalidCurrencyCodeError{currencyCode}
	}
	a.currencyCode = currencyCode

	return nil
}

// MarshalJSON implements the json.Marshaler interface.
func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Number       string `json:"number"`
		CurrencyCode string `json:"currency"`
	}{
		Number:       a.Number(),
		CurrencyCode: a.CurrencyCode(),
	})
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (a *Amount) UnmarshalJSON(data []byte) error {
	aux := struct {
		Number       string `json:"number"`
		CurrencyCode string `json:"currency"`
	}{}
	err := json.Unmarshal(data, &aux)
	if err != nil {
		return err
	}
	_, _, err = a.number.SetString(aux.Number)
	if err != nil {
		return InvalidNumberError{aux.Number}
	}
	if aux.CurrencyCode == "" || !IsValid(aux.CurrencyCode) {
		return InvalidCurrencyCodeError{aux.CurrencyCode}
	}
	a.currencyCode = aux.CurrencyCode

	return nil
}

// Value implements the database/driver.Valuer interface.
//
// Allows storing amounts in a PostgreSQL composite type.
func (a Amount) Value() (driver.Value, error) {
	return fmt.Sprintf("(%v,%v)", a.Number(), a.CurrencyCode()), nil
}

// Scan implements the database/sql.Scanner interface.
//
// Allows scanning amounts from a PostgreSQL composite type.
func (a *Amount) Scan(src interface{}) error {
	// Wire format: "(9.99,USD)".
	input := src.(string)
	if len(input) == 0 {
		return nil
	}
	input = strings.Trim(input, "()")
	values := strings.Split(input, ",")
	n := values[0]
	currencyCode := values[1]
	var tmp apd.Decimal
	_, _, err := tmp.SetString(n)
	if err != nil {
		return InvalidNumberError{n}
	}
	if currencyCode == "" || !IsValid(currencyCode) {
		return InvalidCurrencyCodeError{currencyCode}
	}
	a.number = tmp
	a.currencyCode = currencyCode

	return nil
}

var (
	decimalContextPrecision39 = apd.BaseContext.WithPrecision(39)
	decimalContextPrecision19 = apd.BaseContext.WithPrecision(19)
)

// decimalContext returns the decimal context to use for a calculation.
func decimalContext(decimals ...*apd.Decimal) *apd.Context {
	// Choose between decimal64 (19 digits) and decimal128 (39 digits)
	// based on operand size (> int32), for increased performance.
	for _, d := range decimals {
		if d.Coeff.BitLen() > 31 {
			return decimalContextPrecision39
		}
	}
	return decimalContextPrecision19
}
