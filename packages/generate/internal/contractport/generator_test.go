package contractport

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestGenerateUnaryPortAndRemoteAdapter(t *testing.T) {
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"user/v1/user.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("user/v1/user.proto"),
			Package: proto.String("user.v1"),
			Syntax:  proto.String("proto3"),
			Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/demo/common/user/v1;userv1")},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("GetUserRequest")},
				{Name: proto.String("GetUserResponse")},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("UserService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("GetUser"),
						InputType:  proto.String(".user.v1.GetUserRequest"),
						OutputType: proto.String(".user.v1.GetUserResponse"),
					},
					{
						Name:            proto.String("WatchUsers"),
						InputType:       proto.String(".user.v1.GetUserRequest"),
						OutputType:      proto.String(".user.v1.GetUserResponse"),
						ServerStreaming: proto.Bool(true),
					},
				},
			}},
		}},
	}
	plugin, err := (protogen.Options{}).New(request)
	require.NoError(t, err)
	require.NoError(t, Generate(plugin))
	response := plugin.Response()
	require.Len(t, response.File, 1)
	content := response.File[0].GetContent()

	assert.True(t, strings.HasSuffix(response.File[0].GetName(), "_modular.pb.go"))
	assert.Contains(t, content, "type UserServicePort interface")
	assert.Contains(t, content, "GetUser(ctx context.Context, in *GetUserRequest) (*GetUserResponse, error)")
	assert.Contains(t, content, "func NewRemoteUserService(client UserServiceClient) UserServicePort")
	assert.Contains(t, content, "errs.FromError(err)")
	assert.NotContains(t, content, "grpc.CallOption")
	assert.NotContains(t, content, "WatchUsers")
}
