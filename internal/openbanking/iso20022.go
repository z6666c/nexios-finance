package openbanking

import (
	"encoding/xml"
	"time"

	"nexios-finance/internal/uuid"
)

// Pacs008Message is a simplified ISO 20022 pacs.008 structure for instant
// payment rail integration. This is NOT a complete implementation of the
// official pacs.008.001.08 schema and must be validated against the official
// XSD before any production use.
type Pacs008Message struct {
	XMLName    xml.Name `xml:"Document"`
	MsgID      string   `xml:"FIToFICstmrCdtTrf>GrpHdr>MsgId"`
	CreDtTm    string   `xml:"FIToFICstmrCdtTrf>GrpHdr>CreDtTm"`
	AmountVal  string   `xml:"FIToFICstmrCdtTrf>CdtTrfTxInf>IntrBkSttlmAmt"`
	Currency   string   `xml:"FIToFICstmrCdtTrf>CdtTrfTxInf>IntrBkSttlmAmt>Ccy,attr"`
	DebtorAcct string   `xml:"FIToFICstmrCdtTrf>CdtTrfTxInf>DbtrAcct>Id>IBAN"`
	CredAcct   string   `xml:"FIToFICstmrCdtTrf>CdtTrfTxInf>CdtrAcct>Id>IBAN"`
	EndToEndID string   `xml:"FIToFICstmrCdtTrf>CdtTrfTxInf>PmtId>EndToEndId"`
}

func BuildPacs008(amountMinor int64, currency, debtorIBAN, creditorIBAN, idempotencyKey string) ([]byte, error) {
	amountMajor := float64(amountMinor) / 100.0
	msg := Pacs008Message{
		MsgID:      uuid.New().String(),
		CreDtTm:    time.Now().UTC().Format(time.RFC3339),
		AmountVal:  formatAmount(amountMajor),
		Currency:   currency,
		DebtorAcct: debtorIBAN,
		CredAcct:   creditorIBAN,
		EndToEndID: idempotencyKey,
	}
	return xml.MarshalIndent(msg, "", "  ")
}

func formatAmount(v float64) string {
	cents := int64(v*100 + 0.5)
	return itoaFixed2(cents)
}

func itoaFixed2(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	whole := cents / 100
	frac := cents % 100
	digits := func(n int64) string {
		if n == 0 {
			return "0"
		}
		var b []byte
		for n > 0 {
			b = append([]byte{byte('0' + n%10)}, b...)
			n /= 10
		}
		return string(b)
	}
	fracStr := digits(frac)
	if len(fracStr) < 2 {
		fracStr = "0" + fracStr
	}
	return sign + digits(whole) + "." + fracStr
}
