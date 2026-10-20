package metadata

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. Every module's fragment carries its own module
// name and the version of the fragment's shape.
const (
	fragmentTitle   = "objectstore metadata"
	fragmentVersion = "1.0.0"
)

// The paths the operations are declared at, relative to the API root the Spec
// carries. The mounting resolves them against that root, so the path is written
// once and the fragment keeps the relative form the platform publishes.
const (
	pathObjects        = "/objects"
	pathObject         = "/objects/:id"
	pathObjectContent  = "/objects/:id/content"
	pathObjectComplete = "/objects/:id/complete"
)

// fallbackContentType is the media type an object's bytes are answered with
// when the object records no MIME. An upload records the declared content type,
// so the fallback only covers a completed row that carries none.
const fallbackContentType = "application/octet-stream"

// CreateObjectInput is the request of a declaration: the fields of an
// objectstore.Declaration. It is both the handler's input and the JSON body the
// fragment publishes.
type CreateObjectInput struct {
	_              struct{} `description:"An object to declare before its bytes are uploaded: the name, the exact size and media type, and the checksum the upload is held to."`
	Name           string   `json:"name" description:"An optional label for the object; normalized before storing, and omitted or empty stores an empty name"`
	Size           int      `json:"size" required:"true" description:"The object's size in bytes; must be positive, and the upload's byte count must equal it exactly. A declaration without a positive size is refused whole."`
	ContentType    string   `json:"content_type" required:"true" description:"The media type the bytes will carry; must be non-blank. The upload records it as the object's mime."`
	ChecksumSHA256 string   `json:"checksum_sha256" description:"The declared SHA-256 as 64 hex digits; empty declares no checksum"`
}

// ListObjectsInput is the request of a page read: the cursor and the page cap.
type ListObjectsInput struct {
	Cursor string `query:"cursor" description:"Continue after the last row of the previous page; absent or empty starts at the first page"`
	Limit  int    `query:"limit" description:"Cap on the page size; the metadata module applies a default and an upper bound"`
}

// GetObjectInput is the request of a single-object read.
type GetObjectInput struct {
	ID string `path:"id" description:"The object's identifier, as returned at declaration. An identifier this tenant cannot see, whether unknown, another tenant's or deleted, is answered 404."`
}

// DeleteObjectInput is the request of a deletion.
type DeleteObjectInput struct {
	ID string `path:"id" description:"The object's identifier. An object already deleted is a success, so a repeat needs no different handling."`
}

// UploadObjectContentInput is the request of an upload: the object's identifier
// and the whole request body. The body is bound from the raw bytes, so the bytes
// handed to the store are the ones the request carried; the count the upload is
// checked against is the request's Content-Length, which the handler reads from
// the entry point's Meta rather than from the body's length.
type UploadObjectContentInput struct {
	ID   string `path:"id" description:"The identifier of a pending or uploaded object whose upload window is still open."`
	Body []byte `raw:"true"`
}

// CompleteObjectInput is the request of a completion.
type CompleteObjectInput struct {
	ID string `path:"id" description:"The identifier of an object to move to completed; one already completed is returned unchanged."`
}

// GetObjectContentInput is the request of a byte read.
type GetObjectContentInput struct {
	ID string `path:"id" description:"The identifier of a completed object whose bytes are to be read."`
}

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline, as the hand-written fragment did, rather than as
// a component of its own.
type (
	// objectCreatedBody is the 201 answer: the view of the object just declared.
	objectCreatedBody = struct {
		_      struct{}          `description:"The view of the object just declared; it is pending and its observed size, mime and checksum_sha256 are not filled in yet."`
		Object ObjectstoreObject `json:"object" required:"true" description:"The declared object's view."`
	}

	// objectPageBody is the 200 answer of a list: one page and its next cursor.
	objectPageBody = struct {
		_          struct{}            `description:"One page of the tenant's undeleted objects; next_cursor is empty when the page was not full."`
		Objects    []ObjectstoreObject `json:"objects" required:"true" description:"The page's objects, at most limit of them, ordered by creation instant and then identifier, both ascending."`
		NextCursor string              `json:"next_cursor" required:"true" description:"The cursor to pass on the next call; empty when the page was not full, meaning no further page is known."`
	}

	// objectReadBody is the 200 answer of a single-object read.
	objectReadBody = struct {
		_      struct{}          `description:"The view of the requested object."`
		Object ObjectstoreObject `json:"object" required:"true" description:"The requested object's view."`
	}

	// objectCompletedBody is the 200 answer of a completion.
	objectCompletedBody = struct {
		_      struct{}          `description:"The view of the object after the call; a repeat on an already-completed object returns it unchanged."`
		Object ObjectstoreObject `json:"object" required:"true" description:"The completed object's view."`
	}
)

// createOperation declares POST /objects: a JSON declaration, answered with the
// pending view.
var createOperation = speedhttp.NewOperation[CreateObjectInput, objectCreatedBody](
	"objectstore.metadata.createObject",
	http.MethodPost,
	pathObjects,
	"Declare an object and open its upload window",
	"Declares an object: validates the declaration, normalizes the name once, mints the object identifier and lands a pending row, with an upload window opened from the configured upload-window. size must be positive, content_type must be non-blank, and checksum_sha256 is either empty or 64 hex digits; a declaration that fails any of these is refused whole and lands no row. An omitted, empty or reduced-to-nothing name stores as an empty string rather than failing. The response is the pending view: its size, mime and checksum_sha256 are not filled in until the first upload records them.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The object declaration, as a JSON object.",
		Required:    true,
		Name:        "ObjectstoreDeclaration",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusCreated,
			Description: "The declared object",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The body is not a JSON object or the declaration is not readable"),
		speedhttp.ProblemResponse(http.StatusUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The object store could not answer"),
	},
)

// uploadOperation declares PUT /objects/:id/content: the raw request body is
// stored as the object's bytes. Its handler builds the answer because the rule
// it applies reads the request's Content-Length, which only the entry point's
// Meta carries.
var uploadOperation = speedhttp.NewReplyOperation[UploadObjectContentInput](
	"objectstore.metadata.uploadObjectContent",
	http.MethodPut,
	pathObjectContent,
	"Store the request body as the object's bytes",
	"Stores the request body as the object's bytes under the internal key. The request's Content-Length must be present and equal the declared size: a request that names no length, or whose length differs, is refused before any byte is stored. The object must be pending or uploaded and its upload window must still be open: a completed object or a closed window is a 409, and a deleted object or one this tenant cannot see is a 404. A successful upload records the observed size and SHA-256 and moves the object to uploaded; re-uploading a pending or uploaded object replaces its bytes under the same key.",
	&speedhttp.RequestBody{
		MediaType:   "application/octet-stream",
		Description: "The object's bytes, as the raw request body.",
		Required:    true,
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The bytes were stored",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The request names no Content-Length, or its length does not match the declaration"),
		speedhttp.ProblemResponse(http.StatusUnsupportedMediaType,
			"The request body is not application/octet-stream"),
		speedhttp.ProblemResponse(http.StatusNotFound,
			"No object this tenant can see carries that id"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The object's state does not allow the action, or its upload window has closed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The object store could not answer"),
	},
)

// completeOperation declares POST /objects/:id/complete: an uploaded object is
// moved to completed and answered with its view.
var completeOperation = speedhttp.NewOperation[CompleteObjectInput, objectCompletedBody](
	"objectstore.metadata.completeObject",
	http.MethodPost,
	pathObjectComplete,
	"Move an object to completed",
	"Moves an uploaded object to completed and writes expires_at from the configured object-ttl, leaving it absent when no lifetime is configured; the object's bytes become readable. A pending object was never uploaded and is a 409; an already-completed object is returned unchanged with its expiry as it was, so a repeat is idempotent. A deleted object, an unknown identifier or another tenant's object is a 404.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The completed object",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusNotFound,
			"No object this tenant can see carries that id"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The object's state does not allow the action"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The object store could not answer"),
	},
)

// listOperation declares GET /objects: one page of the tenant's undeleted
// objects.
var listOperation = speedhttp.NewOperation[ListObjectsInput, objectPageBody](
	"objectstore.metadata.listObjects",
	http.MethodGet,
	pathObjects,
	"One page of the tenant's undeleted objects",
	"Lists this tenant's objects that are not deleted, ordered by creation instant and then identifier, both ascending. The cursor is opaque and continues after the last row of the previous page, so rows written while a caller pages do not shift rows already read. limit defaults to 50 rows when absent or zero; a negative limit, one above 200, a non-numeric limit or a cursor this module did not write is refused whole with a 400. A database failure is answered 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "A page of objects and the cursor of the page's last row",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The cursor or limit is not readable"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The object store could not answer"),
	},
)

// getOperation declares GET /objects/:id: one object's view.
var getOperation = speedhttp.NewOperation[GetObjectInput, objectReadBody](
	"objectstore.metadata.getObject",
	http.MethodGet,
	pathObject,
	"One object's metadata",
	"Returns one object's view. A deleted object is answered exactly like an unknown identifier or another tenant's object, with a 404, so the response does not reveal whether the object existed. The view never carries the internal storage key, and every instant is UTC.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The object",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusNotFound,
			"No object this tenant can see carries that id"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The object store could not answer"),
	},
)

// contentOperation declares GET /objects/:id/content: the completed object's
// bytes, streamed with the media type the object records. Its handler builds
// the answer, because a stream is not a typed body the simple form could write
// and because the media type differs from one object to the next. The success
// body is declared as */*, the dynamic media type the handler chooses; the
// fragment publishes it as binary bytes.
var contentOperation = speedhttp.NewReplyOperation[GetObjectContentInput](
	"objectstore.metadata.getObjectContent",
	http.MethodGet,
	pathObjectContent,
	"The object's bytes, streamed with the object's recorded media type",
	"Streams a completed object's bytes with the Content-Type taken from the mime the object recorded at upload, falling back to application/octet-stream when the object records none. Only a completed object can be read: pending or uploaded is a 409, while deleted, unknown or another tenant's is a 404. A completed row whose bytes are missing from the backend is a 503. No Content-Disposition is set; a client that wants a filename takes the view's name.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The object's bytes, written with the media type the object recorded",
			Body:        &speedhttp.Body{MediaType: "*/*"},
		},
		speedhttp.ProblemResponse(http.StatusNotFound,
			"No object this tenant can see carries that id"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The object is not completed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The object store could not answer"),
	},
)

// deleteOperation declares DELETE /objects/:id: the object's bytes are removed
// and its row tombstoned.
var deleteOperation = speedhttp.NewOperation[DeleteObjectInput, struct{}](
	"objectstore.metadata.deleteObject",
	http.MethodDelete,
	pathObject,
	"Remove an object's bytes and tombstone its row",
	"Removes the object's bytes through the byte backend and tombstones its row with state deleted. An object already deleted is a success that touches no byte, so a repeat is idempotent. A byte-backend failure leaves the row unchanged and answers 503. An unknown identifier or another tenant's object is answered 404.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The object was deleted, or was already deleted",
		},
		speedhttp.ProblemResponse(http.StatusNotFound,
			"No object this tenant can see carries that id"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The object store could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	createOperation,
	uploadOperation,
	completeOperation,
	listOperation,
	getOperation,
	contentOperation,
	deleteOperation,
}
