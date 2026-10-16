package udp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"waveloggate/internal/adif"
)

const (
	wsjtxMessageTypeQSOLogged  = 5
	wsjtxMessageTypeLoggedADIF = 12
	qsoLoggedADIFWait          = 100 * time.Millisecond
)

func parseWSJTXQSO(data []byte) (map[string]string, uint32, bool, error) {
	if len(data) < 4 || binary.BigEndian.Uint32(data[:4]) != 0xadbccbda {
		return nil, 0, false, nil
	}
	reader := bytes.NewReader(data[4:])
	var schema, messageType uint32
	if err := binary.Read(reader, binary.BigEndian, &schema); err != nil {
		return nil, 0, true, err
	}
	if err := binary.Read(reader, binary.BigEndian, &messageType); err != nil {
		return nil, 0, true, err
	}
	if messageType != wsjtxMessageTypeQSOLogged && messageType != wsjtxMessageTypeLoggedADIF {
		return nil, messageType, true, nil
	}
	if schema != 2 && schema != 3 {
		return nil, messageType, true, fmt.Errorf("unsupported WSJT-X schema %d", schema)
	}
	clientID, err := readWSJTXString(reader)
	if err != nil {
		return nil, messageType, true, err
	}
	if messageType == wsjtxMessageTypeLoggedADIF {
		fields, binaryPacket, err := parseLoggedADIF(reader, clientID)
		return fields, messageType, binaryPacket, err
	}
	fields, binaryPacket, err := parseQSOLogged(reader, clientID)
	return fields, messageType, binaryPacket, err
}

func parseLoggedADIF(reader *bytes.Reader, clientID string) (map[string]string, bool, error) {
	payload, err := readWSJTXString(reader)
	if err != nil {
		return nil, true, err
	}
	fields := adif.Parse(adif.NormalizeKIndex(adif.NormalizeTXPwr(payload)))
	delete(fields, "ADIF_VER")
	delete(fields, "PROGRAMID")
	ensureFreeDVSubmode(fields, clientID)
	if len(fields) == 0 {
		return nil, true, fmt.Errorf("logged ADIF message carries no QSO fields")
	}
	return fields, true, nil
}

func parseQSOLogged(reader *bytes.Reader, clientID string) (map[string]string, bool, error) {
	fields := make(map[string]string)
	readDate := func(dateField, timeField string) error {
		var julianDay int64
		var milliseconds uint32
		var spec uint8
		for _, value := range []any{&julianDay, &milliseconds, &spec} {
			if err := binary.Read(reader, binary.BigEndian, value); err != nil {
				return err
			}
		}
		if spec != 1 || julianDay < 1721426 || julianDay > 5373484 || milliseconds >= 86400000 {
			return fmt.Errorf("invalid or non-UTC WSJT-X timestamp")
		}
		stamp := time.Unix((julianDay-2440588)*86400, int64(milliseconds)*int64(time.Millisecond)).UTC()
		fields[dateField] = stamp.Format("20060102")
		fields[timeField] = stamp.Format("150405")
		return nil
	}
	readFields := func(names ...string) error {
		for _, name := range names {
			value, err := readWSJTXString(reader)
			if err != nil {
				return err
			}
			if value != "" {
				fields[name] = value
			}
		}
		return nil
	}
	if err := readDate("QSO_DATE_OFF", "TIME_OFF"); err != nil {
		return nil, true, err
	}
	if err := readFields("CALL", "GRIDSQUARE"); err != nil {
		return nil, true, err
	}
	var frequency uint64
	if err := binary.Read(reader, binary.BigEndian, &frequency); err != nil {
		return nil, true, err
	}
	fields["FREQ"] = strconv.FormatFloat(float64(frequency)/1e6, 'f', 6, 64)
	if err := readFields("MODE", "RST_SENT", "RST_RCVD", "TX_PWR", "COMMENT", "NAME"); err != nil {
		return nil, true, err
	}
	if err := readDate("QSO_DATE", "TIME_ON"); err != nil {
		return nil, true, err
	}
	if err := readFields("OPERATOR", "STATION_CALLSIGN", "MY_GRIDSQUARE"); err != nil {
		return nil, true, err
	}
	var optional []string
	for reader.Len() > 0 {
		value, err := readWSJTXString(reader)
		if err != nil {
			break
		}
		optional = append(optional, value)
	}
	if len(optional) >= 3 && optional[2] != "" {
		fields["PROP_MODE"] = optional[2]
	}
	if fields["CALL"] == "" || frequency == 0 || fields["MODE"] == "" {
		return nil, true, fmt.Errorf("QSO logged message is missing callsign, frequency or mode")
	}
	ensureFreeDVSubmode(fields, clientID)
	return fields, true, nil
}

func ensureFreeDVSubmode(fields map[string]string, clientID string) {
	if strings.EqualFold(clientID, "FreeDV") && strings.EqualFold(fields["MODE"], "DIGITALVOICE") && fields["SUBMODE"] == "" {
		fields["SUBMODE"] = "FREEDV"
	}
}

func readWSJTXString(reader *bytes.Reader) (string, error) {
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return "", err
	}
	if length == 0xffffffff {
		return "", nil
	}
	if uint64(length) > uint64(reader.Len()) {
		return "", io.ErrUnexpectedEOF
	}
	value := make([]byte, int(length))
	_, err := io.ReadFull(reader, value)
	return string(value), err
}
