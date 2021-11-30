package hooks

import (
	"fmt"
	"log"
	"net/rpc"
	"os/exec"

	"github.com/hashicorp/go-plugin"
	"github.com/tus/tusd/pkg/handler"
)

// TODO: When the tusd process stops, the plugin does not get properly killed
// and lives on as a zombie process.

type PluginHook struct {
	Path string

	handlerImpl HookHandler
}

func (h *PluginHook) Setup() error {
	// We're a host! Start by launching the plugin process.
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		Cmd:             exec.Command(h.Path),
		//Logger:          logger,
	})
	//defer client.Kill()

	// Connect via RPC
	rpcClient, err := client.Client()
	if err != nil {
		log.Fatal(err)
	}

	// Request the plugin
	raw, err := rpcClient.Dispense("hookHandler")
	if err != nil {
		log.Fatal(err)
	}

	// We should have a HookHandler now! This feels like a normal interface
	// implementation but is in fact over an RPC connection.
	h.handlerImpl = raw.(HookHandler)

	return h.handlerImpl.Setup()
}

func (h *PluginHook) InvokeHook(req HookRequest) (HookResponse, error) {
	return h.handlerImpl.InvokeHook(req)
}

// handshakeConfigs are used to just do a basic handshake between
// a plugin and host. If the handshake fails, a user friendly error is shown.
// This prevents users from executing bad plugins or executing a plugin
// directory. It is a UX feature, not a security feature.
var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "TUSD_PLUGIN",
	MagicCookieValue: "yes",
}

// pluginMap is the map of plugins we can dispense.
var pluginMap = map[string]plugin.Plugin{
	"hookHandler": &HookHandlerPlugin{},
}

// TODO: Explain, mention that it is internal only
// TODO: Do we actually need this? Maybe not...
type InvokeHookRPCAnswer struct {
	HookResponse HookResponse
	TusdError    *handler.Error // Why is TusdError a pointer
}

// Here is an implementation that talks over RPC
type HookHandlerRPC struct{ client *rpc.Client }

func (g *HookHandlerRPC) Setup() error {
	var res interface{}
	err := g.client.Call("Plugin.Setup", new(interface{}), &res)
	return err
}

func (g *HookHandlerRPC) InvokeHook(req HookRequest) (HookResponse, error) {
	var answer InvokeHookRPCAnswer
	err := g.client.Call("Plugin.InvokeHook", req, &answer)
	fmt.Printf("Client: %#v\n", answer.TusdError)
	if err != nil {
		return answer.HookResponse, err
	}

	if answer.TusdError != nil {
		return answer.HookResponse, *answer.TusdError
	}

	return answer.HookResponse, nil
}

// Here is the RPC server that HookHandlerRPC talks to, conforming to
// the requirements of net/rpc
type HookHandlerRPCServer struct {
	// This is the real implementation
	Impl HookHandler
}

func (s *HookHandlerRPCServer) Setup(args interface{}, resp *interface{}) error {
	return s.Impl.Setup()
}

func (s *HookHandlerRPCServer) InvokeHook(args HookRequest, answer *InvokeHookRPCAnswer) error {
	resp, err := s.Impl.InvokeHook(args)
	if err != nil {

		if tusdErr, ok := err.(handler.Error); ok {
			answer.TusdError = &tusdErr
			return nil
		} else {
			return err
		}
	}

	answer.HookResponse = resp

	return nil
}

// This is the implementation of plugin.Plugin so we can serve/consume this
//
// This has two methods: Server must return an RPC server for this plugin
// type. We construct a HookHandlerRPCServer for this.
//
// Client must return an implementation of our interface that communicates
// over an RPC client. We return HookHandlerRPC for this.
//
// Ignore MuxBroker. That is used to create more multiplexed streams on our
// plugin connection and is a more advanced use case.
type HookHandlerPlugin struct {
	// Impl Injection
	Impl HookHandler
}

func (p *HookHandlerPlugin) Server(*plugin.MuxBroker) (interface{}, error) {
	return &HookHandlerRPCServer{Impl: p.Impl}, nil
}

func (HookHandlerPlugin) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &HookHandlerRPC{client: c}, nil
}
