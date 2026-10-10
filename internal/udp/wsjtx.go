package udp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"time"
)

func parseFreeDVQSO(data []byte) (map[string]string, bool, error) {
	if len(data) < 4 || binary.BigEndian.Uint32(data[:4]) != 0xadbccbda {
		return nil, false, nil
	}
	reader := bytes.NewReader(data[4:])
	var schema, messageType uint32
	if err := binary.Read(reader, binary.BigEndian, &schema); err != nil {
		return nil, true, err
	}
	if err := binary.Read(reader, binary.BigEndian, &messageType); err != nil {
		return nil, true, err
	}
	if messageType == 12 {
		return nil, false, nil
	}
	if messageType != 5 {
		return nil, true, nil
	}
	if schema != 2 && schema != 3 {
		return nil, true, fmt.Errorf("unsupported WSJT-X schema %d", schema)
	}
	identifier, err := readWSJTXString(reader)
	if err != nil {
		return nil, true, err
	}
	if identifier != "FreeDV" {
		return nil, true, nil
	}
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
	if fields["CALL"] == "" || frequency == 0 || fields["MODE"] == "" {
		return nil, true, fmt.Errorf("FreeDV QSO is missing callsign, frequency or mode")
	}
	if fields["MODE"] == "DIGITALVOICE" {
		fields["SUBMODE"] = "FREEDV"
	}
	return fields, true, nil
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
