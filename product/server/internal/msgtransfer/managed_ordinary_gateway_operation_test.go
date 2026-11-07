package msgtransfer

import (
	"context"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestManagedOrdinaryGatewayAuthorizationProvidesRPCOperationID(t *testing.T) {
	const manager = "original-fixture-manager"
	const method = msggateway.MsgGateway_SuperGroupOnlineBatchPushOneMsg_FullMethodName
	request := &msggateway.OnlineBatchPushOneMsgReq{MsgData: &sdkws.MsgData{SendID: "sender", RecvID: "peer", SessionType: constant.SingleChatType, Content: []byte("message")}, PushToUserIDs: []string{"peer"}}
	for _, tc := range []struct {
		name        string
		operationID string
	}{
		{name: "generates-operation-for-background-worker"},
		{name: "preserves-existing-operation", operationID: "original-source-operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx := context.WithValue(parent, constant.OpUserID, "prior-actor")
			if tc.operationID != "" {
				ctx = context.WithValue(ctx, constant.OperationID, tc.operationID)
			}
			authorized, err := authorizeManagedOrdinaryGateway(ctx, manager, request)
			if err != nil || authorized == nil {
				t.Fatal("owned gateway authorization failed", err)
			}
			deadline, _ := parent.Deadline()
			calls := 0
			reply := &msggateway.OnlineBatchPushOneMsgResp{}
			err = rpcmiddleware.Client(authorized, method, request, reply, nil, func(outgoing context.Context, calledMethod string, calledRequest, calledReply any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				calls++
				if calledMethod != method || calledRequest != request || calledReply != reply {
					t.Fatal("original gateway invocation changed")
				}
				actualDeadline, ok := outgoing.Deadline()
				if !ok || !actualDeadline.Equal(deadline) || outgoing.Done() != parent.Done() {
					t.Fatal("gateway RPC did not retain its original lifetime")
				}
				md, ok := metadata.FromOutgoingContext(outgoing)
				operations, actors := md.Get(constant.OperationID), md.Get(constant.OpUserID)
				if !ok || len(operations) != 1 || len(actors) != 1 || actors[0] != manager {
					t.Fatal("gateway RPC did not carry one operation and the original manager")
				}
				if tc.operationID == "" {
					if id, err := uuid.Parse(operations[0]); err != nil || id == uuid.Nil {
						t.Fatal("background gateway RPC did not receive a generated operation ID")
					}
				} else if operations[0] != tc.operationID {
					t.Fatal("gateway RPC replaced its original operation ID")
				}
				return nil
			})
			if err != nil || calls != 1 {
				t.Fatal("actual RPC middleware rejected the owned gateway request", err)
			}
			cancel()
			if authorized.Err() != context.Canceled {
				t.Fatal("canceling the owned context did not cancel authorization")
			}
		})
	}
}
