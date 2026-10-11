package udp

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"waveloggate/internal/adif"
	"waveloggate/internal/config"
	"waveloggate/internal/wavelog"
)

func writeWSJTXString(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(value)))
	buffer.WriteString(value)
}

func wsjtxQSO(messageType uint32, client string) []byte {
	var packet bytes.Buffer
	for _, value := range []uint32{0xadbccbda, 3, messageType} {
		_ = binary.Write(&packet, binary.BigEndian, value)
	}
	writeWSJTXString(&packet, client)
	if messageType == 12 {
		writeWSJTXString(&packet, "<CALL:6>DL1ABC <FREQ:8>7.177000 <MODE:12>DIGITALVOICE <QSO_DATE:8>20261009 <TIME_ON:6>235958 <BAND:3>40m <STATION_CALLSIGN:6>DO2BBC <EOR>")
		return packet.Bytes()
	}
	writeDate := func(stamp time.Time) {
		_ = binary.Write(&packet, binary.BigEndian, int64(2440588+stamp.Unix()/86400))
		_ = binary.Write(&packet, binary.BigEndian, uint32((stamp.Hour()*3600+stamp.Minute()*60+stamp.Second())*1000))
		_ = binary.Write(&packet, binary.BigEndian, uint8(1))
	}
	writeDate(time.Date(2026, 10, 10, 12, 1, 0, 0, time.UTC))
	writeWSJTXString(&packet, "DL1ABC")
	writeWSJTXString(&packet, "JO31AB")
	_ = binary.Write(&packet, binary.BigEndian, uint64(7177000))
	for _, value := range []string{"DIGITALVOICE", "59", "57", "", "test", "Name"} {
		writeWSJTXString(&packet, value)
	}
	writeDate(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	for _, value := range []string{"DO2BBC", "DO2BBC", "JO42", "", "", ""} {
		writeWSJTXString(&packet, value)
	}
	return packet.Bytes()
}

func TestParseWSJTXQSOAcceptsAnyClient(t *testing.T) {
	for _, client := range []string{"FreeDV", "WSJT-X", "JTDX", "JS8Call", "MSHV"} {
		fields, recognized, err := parseWSJTXQSO(wsjtxQSO(5, client))
		if err != nil || !recognized {
			t.Fatalf("%s: recognized=%v err=%v", client, recognized, err)
		}
		wantSubmode := ""
		if client == "FreeDV" {
			wantSubmode = "FREEDV"
		}
		if fields["CALL"] != "DL1ABC" || fields["MODE"] != "DIGITALVOICE" || fields["SUBMODE"] != wantSubmode {
			t.Fatalf("%s: unexpected fields: %v", client, fields)
		}
	}
}

func TestParseWSJTXLoggedADIF(t *testing.T) {
	fields, recognized, err := parseWSJTXQSO(wsjtxQSO(12, "FreeDV"))
	if err != nil || !recognized {
		t.Fatalf("recognized=%v err=%v", recognized, err)
	}
	if fields["CALL"] != "DL1ABC" || fields["BAND"] != "40m" || fields["SUBMODE"] != "FREEDV" {
		t.Fatalf("unexpected fields: %v", fields)
	}
}

func TestParseWSJTXLoggedADIFDoesNotGuessSubmode(t *testing.T) {
	fields, recognized, err := parseWSJTXQSO(wsjtxQSO(12, "WSJT-X"))
	if err != nil || !recognized {
		t.Fatalf("recognized=%v err=%v", recognized, err)
	}
	if fields["MODE"] != "DIGITALVOICE" || fields["SUBMODE"] != "" {
		t.Fatalf("unexpected inferred submode: %v", fields)
	}
}

func TestParseWSJTXIgnoresNonQSO(t *testing.T) {
	for _, messageType := range []uint32{0, 1, 2, 6, 99} {
		fields, recognized, err := parseWSJTXQSO(wsjtxQSO(messageType, "WSJT-X"))
		if err != nil || !recognized || fields != nil {
			t.Fatalf("type %d: fields=%v recognized=%v err=%v", messageType, fields, recognized, err)
		}
	}
}

func TestDedupeWindow(t *testing.T) {
	var seen dedupe
	stamp := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if seen.Seen("qso", stamp) {
		t.Fatal("first QSO was marked duplicate")
	}
	if !seen.Seen("qso", stamp.Add(5*time.Second)) {
		t.Fatal("QSO inside dedupe window was not suppressed")
	}
	if seen.Seen("qso", stamp.Add(11*time.Second)) {
		t.Fatal("QSO outside dedupe window was suppressed")
	}
	if qsoKey(map[string]string{"CALL": "DL1ABC", "BAND": "40m", "MODE": "DIGITALVOICE", "SUBMODE": "FREEDV"}) ==
		qsoKey(map[string]string{"CALL": "DL1ABC", "BAND": "40m", "MODE": "DIGITALVOICE", "SUBMODE": "DMR"}) {
		t.Fatal("different digital-voice submodes share a dedupe key")
	}
}

const qtFreeDVQSO = "adbccbda0000000200000005000000064672656544560000000000258e8c0000f23001" +
	"00000006444c31414243000000064a4f3331414200000000006d83280000000c4449474954414c564f494345" +
	"00000002353900000002353700000000000000107465737420786d6c20636f6d6d656e74000000054ac3b67267" +
	"0000000000258e8b052654300100000006444f3242424300000006444f32424243000000044a4f3432" +
	"000000000000000000000000"

func freeDVPacket(t *testing.T, schema uint32) []byte {
	t.Helper()
	var packet bytes.Buffer
	write := func(value any) {
		t.Helper()
		if err := binary.Write(&packet, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	writeString := func(value string) {
		write(uint32(len(value)))
		packet.WriteString(value)
	}
	writeDate := func(stamp time.Time) {
		write(int64(2440588 + stamp.Unix()/86400))
		write(uint32((stamp.Hour()*3600 + stamp.Minute()*60 + stamp.Second()) * 1000))
		write(uint8(1))
	}
	write(uint32(0xadbccbda))
	write(schema)
	write(uint32(5))
	writeString("FreeDV")
	writeDate(time.Date(2026, 10, 10, 0, 1, 2, 0, time.UTC))
	writeString("DL1ABC")
	writeString("JO31AB")
	write(uint64(7177000))
	for _, value := range []string{"DIGITALVOICE", "59", "57", "", "test xml comment", "J\u00f6rg"} {
		writeString(value)
	}
	writeDate(time.Date(2026, 10, 9, 23, 59, 58, 0, time.UTC))
	for _, value := range []string{"DO2BBC", "DO2BBC", "JO42", "", "", ""} {
		writeString(value)
	}
	return packet.Bytes()
}

func TestParseFreeDVQSO(t *testing.T) {
	expected := map[string]string{
		"CALL": "DL1ABC", "GRIDSQUARE": "JO31AB", "FREQ": "7.177000",
		"MODE": "DIGITALVOICE", "SUBMODE": "FREEDV", "RST_SENT": "59", "RST_RCVD": "57",
		"COMMENT": "test xml comment", "NAME": "J\u00f6rg", "QSO_DATE": "20261009", "TIME_ON": "235958",
		"QSO_DATE_OFF": "20261010", "TIME_OFF": "000102", "OPERATOR": "DO2BBC",
		"STATION_CALLSIGN": "DO2BBC", "MY_GRIDSQUARE": "JO42",
	}
	for _, schema := range []uint32{2, 3} {
		packet, err := hex.DecodeString(qtFreeDVQSO)
		if err != nil {
			t.Fatal(err)
		}
		binary.BigEndian.PutUint32(packet[4:8], schema)
		if !bytes.Equal(packet, freeDVPacket(t, schema)) {
			t.Fatal("packet builder does not match the Qt QDataStream reference")
		}
		fields, recognized, err := parseWSJTXQSO(packet)
		if err != nil || !recognized || !reflect.DeepEqual(fields, expected) {
			t.Fatalf("schema %d: fields=%v recognized=%v err=%v", schema, fields, recognized, err)
		}
		if roundTrip := adif.Parse(adif.MapToADIF(fields)); !reflect.DeepEqual(roundTrip, expected) {
			t.Fatalf("ADIF round trip: %v", roundTrip)
		}
	}
}

func TestReadWSJTXString(t *testing.T) {
	for _, length := range []uint32{0, 0xffffffff} {
		var packet bytes.Buffer
		if err := binary.Write(&packet, binary.BigEndian, length); err != nil {
			t.Fatal(err)
		}
		value, err := readWSJTXString(bytes.NewReader(packet.Bytes()))
		if value != "" || err != nil {
			t.Fatalf("empty/null string: value=%q err=%v", value, err)
		}
	}
}

func TestFreeDVUpload(t *testing.T) {
	for _, api := range []struct {
		name, key, path, field, response string
	}{
		{"v1", "testkey", "/index.php/api/qso", "string", `{"status":"created"}`},
		{"v2", "wl2_testkey", "/index.php/api/v2/qso", "adif", `{"data":{}}`},
	} {
		t.Run(api.name, func(t *testing.T) {
			uploads := make(chan map[string]string, 4)
			endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != api.path {
					t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
				}
				var payload map[string]string
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Error(err)
					http.Error(writer, "invalid JSON", http.StatusBadRequest)
					return
				}
				uploads <- adif.Parse(payload[api.field])
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(api.response))
			}))
			defer endpoint.Close()
			profile := &config.Profile{WavelogURL: endpoint.URL + "/index.php", WavelogKey: api.key, WavelogID: "1"}
			var result *wavelog.QSOResult
			server := New(0, wavelog.New(profile, "test"), nil, profile, func(value *wavelog.QSOResult) { result = value }, nil)
			server.handleDatagram(string(freeDVPacket(t, 2)))
			if len(uploads) != 1 {
				t.Fatalf("expected one upload, got %d", len(uploads))
			}
			fields := <-uploads
			for field, expected := range map[string]string{
				"CALL": "DL1ABC", "BAND": "40m", "FREQ": "7.177000", "MODE": "DIGITALVOICE", "SUBMODE": "FREEDV",
				"QSO_DATE": "20261009", "TIME_ON": "235958", "RST_SENT": "59", "RST_RCVD": "57",
			} {
				if fields[field] != expected {
					t.Errorf("%s: got %q, want %q", field, fields[field], expected)
				}
			}
			if result == nil || !result.Success {
				t.Fatalf("expected successful QSO result: %+v", result)
			}
			server.handleDatagram(string(wsjtxQSO(12, "FreeDV")))
			if len(uploads) != 0 {
				t.Fatal("type 5 and type 12 for the same QSO were both uploaded")
			}
			for _, messageType := range []uint32{0, 1, 2} {
				packet := freeDVPacket(t, 2)
				binary.BigEndian.PutUint32(packet[8:12], messageType)
				server.handleDatagram(string(packet))
			}
			if len(uploads) != 0 {
				t.Fatal("non-QSO messages were uploaded")
			}
		})
	}
}

func FuzzParseFreeDVQSO(f *testing.F) {
	packet, err := hex.DecodeString(qtFreeDVQSO)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(packet)
	f.Add(packet[:4])
	f.Add([]byte("<CALL:6>DL1ABC <EOR>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		fields, recognized, err := parseWSJTXQSO(data)
		if fields != nil && (!recognized || err != nil || fields["CALL"] == "" || fields["MODE"] == "") {
			t.Fatalf("invalid successful parse: fields=%v recognized=%v err=%v", fields, recognized, err)
		}
	})
}

func TestFreeDVIgnoresOtherMessages(t *testing.T) {
	for _, messageType := range []uint32{0, 1, 2, 6, 99} {
		packet := freeDVPacket(t, 2)
		binary.BigEndian.PutUint32(packet[8:12], messageType)
		fields, recognized, err := parseWSJTXQSO(packet)
		if !recognized || fields != nil || err != nil {
			t.Fatalf("type %d: %v %v %v", messageType, fields, recognized, err)
		}
	}
	packet := freeDVPacket(t, 2)
	copy(packet[16:22], "WSJT-X")
	if fields, recognized, err := parseWSJTXQSO(packet); err != nil || !recognized || fields["CALL"] != "DL1ABC" {
		t.Fatalf("other client's type 5 should be parsed generically: %v %v %v", fields, recognized, err)
	}
}

func TestFreeDVMalformed(t *testing.T) {
	packet := freeDVPacket(t, 2)
	for length := 4; length < len(packet)-12; length++ {
		if _, recognized, err := parseWSJTXQSO(packet[:length]); !recognized || err == nil {
			t.Fatalf("truncation %d was not rejected", length)
		}
	}
	binary.BigEndian.PutUint32(packet[12:16], 0xfffffffe)
	if _, _, err := parseWSJTXQSO(packet); err == nil {
		t.Fatal("oversized string was accepted")
	}
	if _, _, err := parseWSJTXQSO(freeDVPacket(t, 4)); err == nil {
		t.Fatal("unsupported schema was accepted")
	}
	packet = freeDVPacket(t, 2)
	packet[34] = 0
	if _, _, err := parseWSJTXQSO(packet); err == nil {
		t.Fatal("non-UTC timestamp was accepted")
	}
	packet = freeDVPacket(t, 2)
	binary.BigEndian.PutUint32(packet[30:34], 86400000)
	if _, _, err := parseWSJTXQSO(packet); err == nil {
		t.Fatal("invalid time of day was accepted")
	}
}

func TestUDPExistingFormats(t *testing.T) {
	var loggedADIF bytes.Buffer
	for _, value := range []uint32{0xadbccbda, 3, 12, 6} {
		if err := binary.Write(&loggedADIF, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	loggedADIF.WriteString("WSJT-X")
	text := "<CALL:6>DL1ABC <MODE:3>FT8 <EOR>"
	if err := binary.Write(&loggedADIF, binary.BigEndian, uint32(len(text))); err != nil {
		t.Fatal(err)
	}
	loggedADIF.WriteString(text)
	for _, data := range []string{text, "<xml></xml>"} {
		if _, recognized, err := parseWSJTXQSO([]byte(data)); recognized || err != nil {
			t.Fatalf("existing format was intercepted: %v %v", recognized, err)
		}
	}
	if fields, recognized, err := parseWSJTXQSO([]byte(loggedADIF.String())); err != nil || !recognized || fields["CALL"] != "DL1ABC" {
		t.Fatalf("type 12 ADIF was not parsed: %v %v %v", fields, recognized, err)
	}
	var statuses []string
	server := New(0, nil, nil, nil, nil, func(message string) { statuses = append(statuses, message) })
	server.handleDatagram(text)
	server.handleDatagram(loggedADIF.String())
	server.handleDatagram(string(freeDVPacket(t, 2)))
	if len(statuses) != 0 {
		t.Fatalf("valid packets produced errors: %v", statuses)
	}
}
