package errs

import (
	"net/http"

	"google.golang.org/grpc/codes"
)

const clientClosedStatus = 499

func toGRPCCode(code int) codes.Code {
	switch code {
	case http.StatusOK:
		return codes.OK
	case http.StatusBadRequest:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.Aborted
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusInternalServerError:
		return codes.Internal
	case http.StatusNotImplemented:
		return codes.Unimplemented
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusPreconditionFailed:
		return codes.FailedPrecondition
	case clientClosedStatus:
		return codes.Canceled
	default:
		return codes.Unknown
	}
}

func fromGRPCCode(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.Canceled:
		return clientClosedStatus
	case codes.InvalidArgument, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists, codes.Aborted:
		return http.StatusConflict
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.FailedPrecondition:
		return http.StatusPreconditionFailed
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.Unknown, codes.Internal, codes.DataLoss:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func BadRequest(message Message, opts ...Option) *Error {
	return New(http.StatusBadRequest, message, opts...)
}

func Unauthorized(message Message, opts ...Option) *Error {
	return New(http.StatusUnauthorized, message, opts...)
}

func Forbidden(message Message, opts ...Option) *Error {
	return New(http.StatusForbidden, message, opts...)
}

func NotFound(message Message, opts ...Option) *Error {
	return New(http.StatusNotFound, message, opts...)
}

func Conflict(message Message, opts ...Option) *Error {
	return New(http.StatusConflict, message, opts...)
}

func TooManyRequests(message Message, opts ...Option) *Error {
	return New(http.StatusTooManyRequests, message, opts...)
}

func ClientClosed(message Message, opts ...Option) *Error {
	return New(clientClosedStatus, message, opts...)
}

func InternalServer(message Message, opts ...Option) *Error {
	return New(http.StatusInternalServerError, message, opts...)
}

func ServiceUnavailable(message Message, opts ...Option) *Error {
	return New(http.StatusServiceUnavailable, message, opts...)
}

func GatewayTimeout(message Message, opts ...Option) *Error {
	return New(http.StatusGatewayTimeout, message, opts...)
}
