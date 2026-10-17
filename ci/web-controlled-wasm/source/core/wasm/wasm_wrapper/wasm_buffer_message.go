//go:build js && wasm

package wasm_wrapper

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall/js"

	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk"
	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk_callback"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/sdkerrs"
	"github.com/openimsdk/openim-sdk-core/v3/wasm/bufferplan"
	"github.com/openimsdk/openim-sdk-core/v3/wasm/event_listener"
)

var bufferState = struct {
	sync.Mutex
	epoch     uint64
	accepting bool
	serial    uint64
	messages  map[string]*bufferLease
	files     map[string]*bufferLease
}{messages: make(map[string]*bufferLease), files: make(map[string]*bufferLease)}

type bufferLease struct {
	epoch, token, clientID string
	files                  []bufferplan.File
}

// Synchronous invalidation precedes SDK init/login/logout work, including the
// logout network wait. No credentials or LoginMgr pointers enter this registry.
func InvalidateBufferSession() {
	bufferState.Lock()
	bufferState.epoch++
	bufferState.accepting = false
	epoch := strconv.FormatUint(bufferState.epoch, 10)
	bufferState.messages = make(map[string]*bufferLease)
	bufferState.files = make(map[string]*bufferLease)
	bufferState.Unlock()
	// Optional for historical bundles; required by the final controlled ownread composite.
	if fn := js.Global().Get("wasmOwnReadTimeSessionInvalidate"); fn.Type() == js.TypeFunction {
		fn.Invoke()
	}
	if fn := js.Global().Get("wasmBufferSessionInvalidate"); fn.Type() == js.TypeFunction {
		fn.Invoke(epoch)
	}
}

func bufferEpoch() uint64 { bufferState.Lock(); defer bufferState.Unlock(); return bufferState.epoch }

func bufferAccepting() bool {
	bufferState.Lock()
	defer bufferState.Unlock()
	return bufferState.accepting
}

func captureBufferSession(epoch uint64) (func() bool, string) {
	mgr := open_im_sdk.UserForSDK
	if mgr == nil || mgr.BaseCtx() == nil || mgr.Conversation() == nil || mgr.GetLoginUserID() == "" {
		return func() bool { return false }, ""
	}
	base, conversation, owner := mgr.BaseCtx(), mgr.Conversation(), mgr.GetLoginUserID()
	current := func() bool {
		return bufferAccepting() && bufferEpoch() == epoch && open_im_sdk.UserForSDK == mgr && mgr.BaseCtx() == base &&
			mgr.Conversation() == conversation && base.Err() == nil && open_im_sdk.GetLoginStatus("") == open_im_sdk.Logged && mgr.GetLoginUserID() == owner
	}
	return current, owner
}

type bufferLoginCallback struct {
	*event_listener.BaseCallback
	epoch uint64
	owner string
}

func (b *bufferLoginCallback) OnSuccess(data string) {
	if open_im_sdk.GetLoginStatus("") == open_im_sdk.Logged && open_im_sdk.GetLoginUserID() == b.owner {
		bufferState.Lock()
		if bufferState.epoch == b.epoch {
			bufferState.accepting = true
		}
		bufferState.Unlock()
	}
	b.BaseCallback.OnSuccess(data)
}
func newBufferLoginCallback(base *event_listener.BaseCallback, args []js.Value) *bufferLoginCallback {
	owner := ""
	if len(args) > 1 && args[1].Type() == js.TypeString {
		owner = args[1].String()
	}
	return &bufferLoginCallback{BaseCallback: base, epoch: bufferEpoch(), owner: owner}
}

// Both public send entries call this before starting any goroutine. The JS
// claim is synchronous, so later ByFile creation cannot enqueue an overwrite.
func claimMessageInvocation(raw string, epoch uint64) (lease *bufferLease, err error) {
	client, ids := bufferplan.UUIDs(raw)
	if !bufferplan.ID(client) {
		return nil, bufferplan.ErrPlan
	}
	plan := bufferplan.Plan{}
	plan.Message.ClientMsgID = client
	for _, id := range ids {
		if !bufferplan.UUID(id) {
			return nil, bufferplan.ErrPlan
		}
		plan.Files = append(plan.Files, bufferplan.File{UUID: id})
	}
	lease, err = claimBuffer(epoch, plan)
	if err != nil {
		return nil, err
	}
	defer func() {
		if recover() != nil {
			forgetBuffer(lease)
			lease = nil
			err = errors.New("media entry claim unavailable")
		}
	}()
	if len(ids) != 0 {
		values := make([]interface{}, len(ids))
		for i, id := range ids {
			values[i] = id
		}
		if !js.Global().Call("wasmBufferFileMapClaim", lease.epoch, lease.token, values).Bool() {
			forgetBuffer(lease)
			return nil, errors.New("media UUID already in flight")
		}
	}
	return lease, nil
}

func claimBuffer(epoch uint64, plan bufferplan.Plan) (*bufferLease, error) {
	bufferState.Lock()
	defer bufferState.Unlock()
	if bufferState.epoch != epoch || len(bufferState.messages) >= 1024 || bufferState.messages[plan.Message.ClientMsgID] != nil {
		return nil, errors.New("buffer operation already active or session retired")
	}
	for _, file := range plan.Files {
		if bufferState.files[file.UUID] != nil {
			return nil, errors.New("buffer UUID already active")
		}
	}
	bufferState.serial++
	lease := &bufferLease{epoch: strconv.FormatUint(epoch, 10), token: strconv.FormatUint(epoch, 10) + ":" + strconv.FormatUint(bufferState.serial, 10), clientID: plan.Message.ClientMsgID, files: plan.Files}
	bufferState.messages[lease.clientID] = lease
	for _, file := range lease.files {
		bufferState.files[file.UUID] = lease
	}
	return lease, nil
}

func forgetBuffer(lease *bufferLease) {
	bufferState.Lock()
	defer bufferState.Unlock()
	if bufferState.messages[lease.clientID] == lease {
		delete(bufferState.messages, lease.clientID)
	}
	for _, file := range lease.files {
		if bufferState.files[file.UUID] == lease {
			delete(bufferState.files, file.UUID)
		}
	}
}

func bufferMessageConflict(raw string) bool {
	clientID, ids := bufferplan.UUIDs(raw)
	bufferState.Lock()
	defer bufferState.Unlock()
	if clientID != "" && bufferState.messages[clientID] != nil {
		return true
	}
	for _, id := range ids {
		if id != "" && bufferState.files[id] != nil {
			return true
		}
	}
	return false
}

func awaitBufferBridge(name string, arguments ...interface{}) (value js.Value, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("buffer bridge invocation failed")
		}
	}()
	settled := make(chan bool, 1)
	then := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		if len(args) != 1 {
			err = errors.New("buffer bridge acknowledgement missing")
		} else {
			value = args[0]
		}
		settled <- true
		return nil
	})
	failed := js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
		err = errors.New("buffer bridge acknowledgement failed")
		settled <- true
		return nil
	})
	defer then.Release()
	defer failed.Release()
	// The pinned JS bridge uses the existing RPC's 5s timeout. Its Promise always
	// settles; do not release Go callbacks early while a late Promise still owns them.
	js.Global().Call(name, arguments...).Call("then", then).Call("catch", failed)
	<-settled
	return value, err
}

func registerBuffer(lease *bufferLease, plan bufferplan.Plan, buffers [2]js.Value) error {
	entries := make([]interface{}, 0, len(plan.Files))
	for _, file := range plan.Files {
		entries = append(entries, map[string]interface{}{"uuid": file.UUID, "size": file.Size, "buffer": buffers[file.Slot]})
	}
	reply, err := awaitBufferBridge("wasmBufferFileMapSet", lease.epoch, lease.token, entries)
	if err != nil {
		return err
	}
	var ack struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			Epoch string            `json:"epoch"`
			Token string            `json:"token"`
			Files []bufferplan.File `json:"files"`
		} `json:"data"`
	}
	if reply.Type() != js.TypeString || json.Unmarshal([]byte(reply.String()), &ack) != nil || ack.ErrCode != 0 ||
		ack.Data.Epoch != lease.epoch || ack.Data.Token != lease.token || len(ack.Data.Files) != len(plan.Files) {
		return errors.New("buffer bridge acknowledgement differs")
	}
	for i, file := range plan.Files {
		if ack.Data.Files[i].UUID != file.UUID || ack.Data.Files[i].Size != file.Size {
			return errors.New("buffer bridge file acknowledgement differs")
		}
	}
	return nil
}

func reserveOrdinary(lease *bufferLease) error {
	reply, err := awaitBufferBridge("wasmBufferFileMapReserve", lease.epoch, lease.token)
	if err != nil {
		return err
	}
	var ack struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			Epoch string            `json:"epoch"`
			Token string            `json:"token"`
			Files []bufferplan.File `json:"files"`
		} `json:"data"`
	}
	if reply.Type() != js.TypeString || json.Unmarshal([]byte(reply.String()), &ack) != nil || ack.ErrCode != 0 || ack.Data.Epoch != lease.epoch || ack.Data.Token != lease.token || len(ack.Data.Files) != len(lease.files) {
		return errors.New("ordinary media reservation not acknowledged")
	}
	for i, file := range lease.files {
		if ack.Data.Files[i].UUID != file.UUID || ack.Data.Files[i].Size < 0 || ack.Data.Files[i].Size > 2147483647 {
			return errors.New("ordinary media reservation differs")
		}
	}
	return nil
}

func (w *WrapperConMsg) sendMessageWithReservation(args []js.Value, callback *event_listener.SendMessageCallback) interface{} {
	_, ids := bufferplan.UUIDs(args[1].String())
	if len(ids) == 0 {
		return event_listener.NewCaller(open_im_sdk.SendMessage, callback, &args).AsyncCallWithCallback()
	}
	epoch := bufferEpoch()
	current, owner := captureBufferSession(epoch)
	var lease *bufferLease
	var err error
	var declared struct {
		SendID string `json:"sendID"`
	}
	if json.Unmarshal([]byte(args[1].String()), &declared) != nil || (declared.SendID != "" && declared.SendID != owner) {
		return callback.HandlerFunc(func() {
			callback.SetOperationID(args[0].String())
			callback.OnError(sdkerrs.ArgsError, "ordinary media sender differs")
		})
	}
	if current() {
		lease, err = claimMessageInvocation(args[1].String(), epoch)
	} else {
		err = errors.New("original media session retired")
	}
	callback.SetOperationID(args[0].String())
	return callback.HandlerFunc(func() {
		go func() {
			if err != nil {
				callback.OnError(sdkerrs.ArgsError, "media already active or session retired")
				return
			}
			var once sync.Once
			finished := func() { once.Do(func() { go releaseBuffer(lease) }) }
			ready := func() bool { return current() && registeredBuffer(lease) }
			cb := &bufferCallbacks{SendMsgCallBack: callback, current: ready, finished: finished}
			defer func() {
				if recover() != nil {
					cb.OnError(sdkerrs.ArgsError, "ordinary media reservation failed")
				}
			}()
			if len(args) < 5 {
				cb.OnError(sdkerrs.ArgsError, "ordinary send arguments incomplete")
				return
			}
			for _, arg := range args[:5] {
				if arg.Type() != js.TypeString {
					cb.OnError(sdkerrs.ArgsError, "ordinary send string arguments required")
					return
				}
			}
			online := false
			if len(args) > 5 {
				if args[5].Type() != js.TypeBoolean {
					cb.OnError(sdkerrs.ArgsError, "ordinary online flag must be boolean")
					return
				}
				online = args[5].Bool()
			}
			if !current() {
				cb.OnError(sdkerrs.ArgsError, "original media session retired")
				return
			}
			if reserveOrdinary(lease) != nil {
				cb.OnError(sdkerrs.ArgsError, "ordinary media reservation not acknowledged")
				return
			}
			if !ready() {
				cb.OnError(sdkerrs.ArgsError, "original media session retired")
				return
			}
			open_im_sdk.SendOrdinaryMessageWithBufferScope(cb, args[0].String(), args[1].String(), args[2].String(), args[3].String(), args[4].String(), online, ready)
		}()
	})
}

func registeredBuffer(lease *bufferLease) (current bool) {
	defer func() {
		if recover() != nil {
			current = false
		}
	}()
	return js.Global().Call("wasmBufferFileMapCurrent", lease.epoch, lease.token).Bool()
}

func releaseBuffer(lease *bufferLease) {
	if len(lease.files) != 0 {
		reply, err := awaitBufferBridge("wasmBufferFileMapRelease", lease.epoch, lease.token)
		if err != nil {
			return
		} // Unknown cleanup retains only a bounded UUID/clientID lock until session retirement.
		var ack struct {
			ErrCode int `json:"errCode"`
			Data    struct {
				Epoch string `json:"epoch"`
				Token string `json:"token"`
			} `json:"data"`
		}
		if reply.Type() != js.TypeString || json.Unmarshal([]byte(reply.String()), &ack) != nil || ack.ErrCode != 0 || ack.Data.Epoch != lease.epoch || ack.Data.Token != lease.token {
			return
		}
	}
	forgetBuffer(lease)
}

type bufferCallbacks struct {
	open_im_sdk_callback.SendMsgCallBack
	once     sync.Once
	done     atomic.Bool
	current  func() bool
	finished func()
}

func (b *bufferCallbacks) OnSuccess(data string) {
	b.once.Do(func() { b.done.Store(true); b.finished(); b.SendMsgCallBack.OnSuccess(data) })
}
func (b *bufferCallbacks) OnError(code int32, message string) {
	b.once.Do(func() { b.done.Store(true); b.finished(); b.SendMsgCallBack.OnError(code, message) })
}
func (b *bufferCallbacks) OnProgress(progress int) {
	if !b.done.Load() && b.current() {
		b.SendMsgCallBack.OnProgress(progress)
	}
}

type bufferDependencies struct {
	current    func() bool
	register   func(*bufferLease, bufferplan.Plan, [2]js.Value) error
	registered func(*bufferLease) bool
	release    func(*bufferLease)
	dispatch   func(open_im_sdk_callback.SendMsgCallBack, string, string, string, string, string, func() bool)
}

// Kept narrow so isolated WASM tests exercise the production sequencing with
// only transport/session/SDK-dispatch boundaries substituted, never a fake plan.
func runBuffer(plan bufferplan.Plan, epoch uint64, operation, recv, group, push string, buffers [2]js.Value, callback open_im_sdk_callback.SendMsgCallBack, deps bufferDependencies) {
	if !deps.current() {
		callback.OnError(sdkerrs.ArgsError, "buffer session expired")
		return
	}
	lease, err := claimBuffer(epoch, plan)
	if err != nil {
		callback.OnError(sdkerrs.ArgsError, err.Error())
		return
	}
	runClaimedBuffer(plan, lease, operation, recv, group, push, buffers, callback, deps)
}

func runClaimedBuffer(plan bufferplan.Plan, lease *bufferLease, operation, recv, group, push string, buffers [2]js.Value, callback open_im_sdk_callback.SendMsgCallBack, deps bufferDependencies) {
	var err error
	var finish sync.Once
	finished := func() { finish.Do(func() { go deps.release(lease) }) }
	current := func() bool { return deps.current() && (len(plan.Files) == 0 || deps.registered(lease)) }
	cb := &bufferCallbacks{SendMsgCallBack: callback, current: current, finished: finished}
	defer func() {
		if recover() != nil {
			cb.OnError(sdkerrs.ArgsError, "buffer preparation failed")
		}
	}()
	if len(plan.Files) != 0 {
		if err = deps.register(lease, plan, buffers); err != nil {
			cb.OnError(sdkerrs.ArgsError, "buffer registration not acknowledged")
			return
		}
	}
	if !current() {
		cb.OnError(sdkerrs.ArgsError, "buffer session expired after registration")
		return
	}
	deps.dispatch(cb, operation, plan.JSON, recv, group, push, current)
}

func bufferLength(value js.Value) (length int64, err error) {
	if value.IsNull() || value.IsUndefined() {
		return -1, nil
	}
	defer func() {
		if recover() != nil {
			err = bufferplan.ErrPlan
		}
	}()
	getter := js.Global().Get("Object").Call("getOwnPropertyDescriptor", js.Global().Get("ArrayBuffer").Get("prototype"), "byteLength").Get("get")
	actual := getter.Call("call", value).Float()
	if actual < 0 || actual > 2147483647 {
		return 0, bufferplan.ErrPlan
	}
	return int64(actual), nil
}

func (w *WrapperConMsg) SendMessageByBuffer(_ js.Value, input []js.Value) interface{} {
	args := append([]js.Value(nil), input...)
	// Capture before HandlerFunc starts a goroutine or any registration awaits.
	epoch := bufferEpoch()
	current, owner := captureBufferSession(epoch)
	var reservation *bufferLease
	var reservationError error
	if len(args) > 1 && args[1].Type() == js.TypeString && current() {
		reservation, reservationError = claimMessageInvocation(args[1].String(), epoch)
	} else {
		reservationError = errors.New("original buffer session unavailable")
	}
	callback := event_listener.NewSendMessageCallback("sendMessageByBuffer", w.commonFunc)
	return callback.HandlerFunc(func() {
		go func() {
			started := false
			defer func() {
				if reservation != nil && !started {
					go releaseBuffer(reservation)
				}
				if recover() != nil {
					callback.OnError(sdkerrs.ArgsError, "invalid buffer arguments")
				}
			}()
			if reservationError != nil {
				callback.OnError(sdkerrs.ArgsError, "media already active or original session unavailable")
				return
			}
			if len(args) != 7 {
				callback.OnError(sdkerrs.ArgsError, "seven buffer arguments required")
				return
			}
			for _, arg := range args[:5] {
				if arg.Type() != js.TypeString {
					callback.OnError(sdkerrs.ArgsError, "buffer string arguments required")
					return
				}
			}
			operation, raw, recv, group, push := args[0].String(), args[1].String(), args[2].String(), args[3].String(), args[4].String()
			callback.SetOperationID(operation)
			if !bufferplan.ID(operation) || len(push) > 1024*1024 {
				callback.OnError(sdkerrs.ArgsError, "invalid operation or push data")
				return
			}
			var pushObject map[string]json.RawMessage
			if json.Unmarshal([]byte(push), &pushObject) != nil || pushObject == nil {
				callback.OnError(sdkerrs.ArgsError, "push object required")
				return
			}
			var lengths [2]int64
			for i := range lengths {
				n, err := bufferLength(args[5+i])
				if err != nil {
					callback.OnError(sdkerrs.ArgsError, "ArrayBuffer required")
					return
				}
				lengths[i] = n
			}
			plan, err := bufferplan.Prepare(raw, recv, group, lengths)
			if err != nil {
				callback.OnError(sdkerrs.ArgsError, "invalid media buffer metadata")
				return
			}
			callback.SetClientMsgID(&args)
			if !current() {
				callback.OnError(sdkerrs.ArgsError, "buffer login required or original session retired")
				return
			}
			if plan.Message.SendID != "" && plan.Message.SendID != owner {
				callback.OnError(sdkerrs.ArgsError, "buffer sender differs")
				return
			}
			reservation.files = plan.Files
			started = true
			runClaimedBuffer(plan, reservation, operation, recv, group, push, [2]js.Value{args[5], args[6]}, callback,
				bufferDependencies{current: current, register: registerBuffer, registered: registeredBuffer, release: releaseBuffer, dispatch: open_im_sdk.SendBufferMessage})
		}()
	})
}
