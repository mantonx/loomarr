package fillervisualsafety

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

const (
	PortableWireProtocolVersion  = 1
	MaximumPortableMetadataBytes = 64 << 10
	portableWireHeaderBytes      = 16
	portableWireRequestKind      = 1
	portableWireResponseKind     = 2
)

var portableWireMagic = [4]byte{'L', 'V', 'S', 'P'}

// WritePortableFrameRequest writes one bounded metadata frame followed by the
// exact RGB payload. A long-lived worker may receive multiple frames serially.
func WritePortableFrameRequest(writer io.Writer, request PortableFrameRequest, payload []byte) error {
	if writer == nil || request.SHA256 == "" || request.SHA256 != PortableFrameRequestSHA256(request) ||
		ValidatePortableFramePayload(request, payload) != nil {
		return errors.New("portable visual-safety wire request is invalid")
	}
	return writePortableWireFrame(writer, portableWireRequestKind, request, payload)
}

// ReadPortableFrameRequest reads exactly one request frame. The caller still
// validates it against the expected capability and coverage plan.
func ReadPortableFrameRequest(reader io.Reader, maximumPayloadBytes int64) (PortableFrameRequest, []byte, error) {
	var request PortableFrameRequest
	payload, err := readPortableWireFrame(reader, portableWireRequestKind, maximumPayloadBytes, &request)
	if err != nil || request.SHA256 == "" || request.SHA256 != PortableFrameRequestSHA256(request) ||
		ValidatePortableFramePayload(request, payload) != nil {
		return PortableFrameRequest{}, nil, errors.New("portable visual-safety wire request could not be read")
	}
	return request, payload, nil
}

// WritePortableFrameResponse writes one metadata-only successful response.
func WritePortableFrameResponse(writer io.Writer, response PortableFrameResponse) error {
	if writer == nil || response.SHA256 == "" || response.SHA256 != PortableFrameResponseSHA256(response) {
		return errors.New("portable visual-safety wire response is invalid")
	}
	return writePortableWireFrame(writer, portableWireResponseKind, response, nil)
}

// ReadPortableFrameResponse reads exactly one metadata-only response. The
// caller still validates model identity and output shape against the request.
func ReadPortableFrameResponse(reader io.Reader) (PortableFrameResponse, error) {
	var response PortableFrameResponse
	payload, err := readPortableWireFrame(reader, portableWireResponseKind, 0, &response)
	if err != nil || len(payload) != 0 || response.SHA256 == "" ||
		response.SHA256 != PortableFrameResponseSHA256(response) {
		return PortableFrameResponse{}, errors.New("portable visual-safety wire response could not be read")
	}
	return response, nil
}

func writePortableWireFrame(writer io.Writer, kind byte, metadata any, payload []byte) error {
	raw, err := json.Marshal(metadata)
	if err != nil || len(raw) == 0 || len(raw) > MaximumPortableMetadataBytes || len(payload) > int(MaximumFrameBytes) {
		return errors.New("portable visual-safety wire frame exceeds its bounds")
	}
	var header [portableWireHeaderBytes]byte
	copy(header[0:4], portableWireMagic[:])
	header[4] = PortableWireProtocolVersion
	header[5] = kind
	binary.BigEndian.PutUint32(header[8:12], uint32(len(raw)))
	binary.BigEndian.PutUint32(header[12:16], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	if err := writeAll(writer, raw); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func readPortableWireFrame(reader io.Reader, expectedKind byte, maximumPayloadBytes int64, into any) ([]byte, error) {
	if reader == nil || maximumPayloadBytes < 0 || maximumPayloadBytes > MaximumFrameBytes {
		return nil, errors.New("portable visual-safety wire reader has invalid bounds")
	}
	var header [portableWireHeaderBytes]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	if !bytes.Equal(header[0:4], portableWireMagic[:]) || header[4] != PortableWireProtocolVersion ||
		header[5] != expectedKind || header[6] != 0 || header[7] != 0 {
		return nil, errors.New("portable visual-safety wire header is invalid")
	}
	metadataBytes := int64(binary.BigEndian.Uint32(header[8:12]))
	payloadBytes := int64(binary.BigEndian.Uint32(header[12:16]))
	if metadataBytes <= 0 || metadataBytes > MaximumPortableMetadataBytes ||
		payloadBytes < 0 || payloadBytes > maximumPayloadBytes {
		return nil, errors.New("portable visual-safety wire frame exceeds its declared bounds")
	}
	metadata := make([]byte, metadataBytes)
	if _, err := io.ReadFull(reader, metadata); err != nil {
		return nil, err
	}
	if err := decodePortableMetadata(metadata, into); err != nil {
		return nil, err
	}
	payload := make([]byte, payloadBytes)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodePortableMetadata(raw []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("portable visual-safety wire metadata has trailing content")
	}
	return nil
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
