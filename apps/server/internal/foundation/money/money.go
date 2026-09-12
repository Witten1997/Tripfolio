// Package money 提供金额字符串的规范化与校验。
// API 以十进制字符串传输金额，本包按币种小数位把 "128.5" 规范化为 "128.50"，
// 并拒绝科学计数法、符号、分组符、超精度与超过 14 位整数的输入（接口设计 1.2、1.3；数据库 §1）。
// 测试样例来自 packages/contracts/fixtures/money.json，与前端共用。
package money

import (
	"errors"
	"strings"
)

// MaxIntegerDigits 是 NUMERIC(18,4) 允许的最大整数位数。
const MaxIntegerDigits = 14

var (
	// ErrInvalidFormat 表示输入不是无符号十进制数字串。
	ErrInvalidFormat = errors.New("money: invalid format")
	// ErrScaleExceeded 表示小数位多于币种允许的位数。
	ErrScaleExceeded = errors.New("money: scale exceeds currency minor units")
	// ErrNegative 表示输入带负号；金额一律以正数输入，方向由类型决定。
	ErrNegative = errors.New("money: negative amount")
	// ErrTooLarge 表示整数位超过 14 位。
	ErrTooLarge = errors.New("money: more than 14 integer digits")
)

// Canonicalize 校验 input 并返回规范形式：去掉整数部分前导零，小数部分补零到 minorUnits 位；
// minorUnits 为 0 时不带小数点。返回的错误可用 errors.Is 与上面四个哨兵比较。
func Canonicalize(input string, minorUnits int) (string, error) {
	if minorUnits < 0 || minorUnits > 4 {
		return "", errors.New("money: minor units must be between 0 and 4")
	}
	if strings.HasPrefix(input, "-") && isUnsignedDecimal(input[1:]) {
		return "", ErrNegative
	}
	if !isUnsignedDecimal(input) {
		return "", ErrInvalidFormat
	}

	intPart, fracPart, _ := strings.Cut(input, ".")
	if len(fracPart) > minorUnits {
		return "", ErrScaleExceeded
	}
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	if len(intPart) > MaxIntegerDigits {
		return "", ErrTooLarge
	}
	if minorUnits == 0 {
		return intPart, nil
	}
	return intPart + "." + fracPart + strings.Repeat("0", minorUnits-len(fracPart)), nil
}

// Compact 校验格式并返回与币种无关的最简形式：去掉整数前导零与小数尾随零（"0128.500" → "128.5"，"5.00" → "5"）。
// 用于在得知币种前计算请求指纹（"128.5" 与 "128.50" 相同）；写入前仍须用 Canonicalize 按币种校验小数位。
func Compact(input string) (string, error) {
	if strings.HasPrefix(input, "-") && isUnsignedDecimal(input[1:]) {
		return "", ErrNegative
	}
	if !isUnsignedDecimal(input) {
		return "", ErrInvalidFormat
	}
	intPart, fracPart, _ := strings.Cut(input, ".")
	if len(fracPart) > 4 {
		return "", ErrScaleExceeded
	}
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	if len(intPart) > MaxIntegerDigits {
		return "", ErrTooLarge
	}
	fracPart = strings.TrimRight(fracPart, "0")
	if fracPart == "" {
		return intPart, nil
	}
	return intPart + "." + fracPart, nil
}

// FromStorage 把数据库读出的定点数（NUMERIC(18,4) 文本，如 "128.5000"）按币种小数位转回规范形式（"128.50"）。
// 存储值在币种小数位之外还有非零尾数时返回 ErrScaleExceeded。
func FromStorage(stored string, minorUnits int) (string, error) {
	intPart, frac, _ := strings.Cut(stored, ".")
	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		return Canonicalize(intPart, minorUnits)
	}
	return Canonicalize(intPart+"."+frac, minorUnits)
}

// isUnsignedDecimal 判断 s 是否符合 ^[0-9]+(\.[0-9]+)?$，只接受 ASCII 数字。
func isUnsignedDecimal(s string) bool {
	if s == "" {
		return false
	}
	seenDot := false
	digitsBefore, digitsAfter := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if seenDot {
				digitsAfter++
			} else {
				digitsBefore++
			}
		case c == '.' && !seenDot:
			seenDot = true
		default:
			return false
		}
	}
	if digitsBefore == 0 {
		return false
	}
	if seenDot && digitsAfter == 0 {
		return false
	}
	return true
}
