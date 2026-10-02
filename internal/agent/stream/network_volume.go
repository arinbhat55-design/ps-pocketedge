package stream

import (
	"context"
	"errors"
	"time"

	dockerclient "github.com/docker/docker/client"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/docker"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// handleListNetworks, handleCreateNetwork, handleRemoveNetwork,
// handleConnectContainerToNetwork, and handleDisconnectContainerFromNetwork
// follow the same shape as the image handlers in session.go: run the
// matching docker/network.go call in this goroutine, then reply with the
// result carrying the command's request_id.

func (r *Runner) handleListNetworks(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.ListNetworksCommand, outbound chan<- *agentv1.AgentMessage) {
	reply := func(networks []docker.NetworkSummary) {
		select {
		case outbound <- networkListResultMessage(cmd.GetRequestId(), networks):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		reply(nil)
		return
	}
	networks, err := docker.ListNetworks(ctx, dockerCli)
	if err != nil {
		r.log.Warn("failed to list networks", "error", err)
	}
	reply(networks)
}

func (r *Runner) handleCreateNetwork(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.CreateNetworkCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), "", errors.New("docker client unavailable on this agent"))
		return
	}
	id, err := docker.CreateNetwork(ctx, dockerCli, cmd.GetName(), cmd.GetDriver(), cmd.GetInternal(), cmd.GetLabels())
	r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), id, err)
}

func (r *Runner) handleRemoveNetwork(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.RemoveNetworkCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), cmd.GetNetworkId(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.RemoveNetwork(ctx, dockerCli, cmd.GetNetworkId())
	r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), cmd.GetNetworkId(), err)
}

func (r *Runner) handleConnectContainerToNetwork(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.ConnectContainerToNetworkCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), cmd.GetNetworkId(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.ConnectContainerToNetwork(ctx, dockerCli, cmd.GetNetworkId(), cmd.GetContainerId())
	r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), cmd.GetNetworkId(), err)
}

func (r *Runner) handleDisconnectContainerFromNetwork(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.DisconnectContainerFromNetworkCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), cmd.GetNetworkId(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.DisconnectContainerFromNetwork(ctx, dockerCli, cmd.GetNetworkId(), cmd.GetContainerId(), cmd.GetForce())
	r.replyNetworkOp(ctx, outbound, cmd.GetRequestId(), cmd.GetNetworkId(), err)
}

// replyNetworkOp sends a NetworkOpResult for requestID onto outbound,
// success iff err is nil — the network-op counterpart to replyOp/replyImageOp.
func (r *Runner) replyNetworkOp(ctx context.Context, outbound chan<- *agentv1.AgentMessage, requestID, networkID string, err error) {
	select {
	case outbound <- networkOpResultMessage(requestID, networkID, err):
	case <-ctx.Done():
	}
}

// handleListVolumes, handleCreateVolume, handleRemoveVolume, and
// handleInspectVolume mirror the network handlers above.

func (r *Runner) handleListVolumes(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.ListVolumesCommand, outbound chan<- *agentv1.AgentMessage) {
	reply := func(volumes []docker.VolumeSummary) {
		select {
		case outbound <- volumeListResultMessage(cmd.GetRequestId(), volumes):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		reply(nil)
		return
	}
	volumes, err := docker.ListVolumes(ctx, dockerCli)
	if err != nil {
		r.log.Warn("failed to list volumes", "error", err)
	}
	reply(volumes)
}

func (r *Runner) handleCreateVolume(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.CreateVolumeCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyVolumeOp(ctx, outbound, cmd.GetRequestId(), cmd.GetName(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.CreateVolume(ctx, dockerCli, cmd.GetName(), cmd.GetDriver(), cmd.GetLabels())
	r.replyVolumeOp(ctx, outbound, cmd.GetRequestId(), cmd.GetName(), err)
}

func (r *Runner) handleRemoveVolume(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.RemoveVolumeCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyVolumeOp(ctx, outbound, cmd.GetRequestId(), cmd.GetName(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.RemoveVolume(ctx, dockerCli, cmd.GetName(), cmd.GetForce())
	r.replyVolumeOp(ctx, outbound, cmd.GetRequestId(), cmd.GetName(), err)
}

func (r *Runner) handleInspectVolume(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.InspectVolumeCommand, outbound chan<- *agentv1.AgentMessage) {
	reply := func(d docker.VolumeDetail) {
		select {
		case outbound <- volumeDetailMessage(cmd.GetRequestId(), d):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		reply(docker.VolumeDetail{Found: false, ErrorMessage: "docker client unavailable on this agent"})
		return
	}
	reply(docker.InspectVolume(ctx, dockerCli, cmd.GetName()))
}

// replyVolumeOp sends a VolumeOpResult for requestID onto outbound, success
// iff err is nil — the volume-op counterpart to replyNetworkOp above.
func (r *Runner) replyVolumeOp(ctx context.Context, outbound chan<- *agentv1.AgentMessage, requestID, name string, err error) {
	select {
	case outbound <- volumeOpResultMessage(requestID, name, err):
	case <-ctx.Done():
	}
}

func (r *Runner) handleVolumeFile(sessionCtx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.VolumeFileCommand, outbound chan<- *agentv1.AgentMessage) {
	result := &agentv1.VolumeFileResult{RequestId: cmd.GetRequestId()}
	defer func() {
		select {
		case outbound <- &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_VolumeFileResult{VolumeFileResult: result}}:
		case <-sessionCtx.Done():
		}
	}()
	if dockerCli == nil {
		result.ErrorMessage = "docker client unavailable on this agent"
		return
	}
	timeout := 2 * time.Minute
	if cmd.GetOperation() == "clone" {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(sessionCtx, timeout)
	defer cancel()
	var err error
	switch cmd.GetOperation() {
	case "list":
		var entries []docker.VolumeFileEntry
		entries, err = docker.ListVolumeFiles(ctx, dockerCli, cmd.GetVolumeName(), cmd.GetPath())
		for _, entry := range entries {
			result.Entries = append(result.Entries, &agentv1.VolumeFileEntry{Name: entry.Name, IsDirectory: entry.IsDirectory, IsSymlink: entry.IsSymlink, SizeBytes: entry.SizeBytes})
		}
	case "read":
		result.Content, err = docker.ReadVolumeFile(ctx, dockerCli, cmd.GetVolumeName(), cmd.GetPath())
	case "write":
		err = docker.WriteVolumeFile(ctx, dockerCli, cmd.GetVolumeName(), cmd.GetPath(), cmd.GetContent())
	case "clone":
		err = docker.CloneVolume(ctx, dockerCli, cmd.GetVolumeName(), cmd.GetTargetVolume())
	default:
		err = errors.New("unknown volume file operation")
	}
	result.Success = err == nil
	if err != nil {
		result.ErrorMessage = err.Error()
	}
}

func networkListResultMessage(requestID string, networks []docker.NetworkSummary) *agentv1.AgentMessage {
	out := make([]*agentv1.NetworkSummary, len(networks))
	for i, n := range networks {
		out[i] = &agentv1.NetworkSummary{
			Id:           n.ID,
			Name:         n.Name,
			Driver:       n.Driver,
			Scope:        n.Scope,
			Internal:     n.Internal,
			Labels:       n.Labels,
			ContainerIds: n.ContainerIDs,
		}
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_NetworkListResult{
			NetworkListResult: &agentv1.NetworkListResult{RequestId: requestID, Networks: out},
		},
	}
}

func networkOpResultMessage(requestID, networkID string, err error) *agentv1.AgentMessage {
	result := &agentv1.NetworkOpResult{
		RequestId: requestID,
		Success:   err == nil,
		NetworkId: networkID,
	}
	if err != nil {
		result.ErrorMessage = err.Error()
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_NetworkOpResult{NetworkOpResult: result},
	}
}

func volumeSummaryToProto(v docker.VolumeSummary) *agentv1.VolumeSummary {
	return &agentv1.VolumeSummary{
		Name:        v.Name,
		Driver:      v.Driver,
		Mountpoint:  v.Mountpoint,
		Labels:      v.Labels,
		SizeBytes:   v.SizeBytes,
		InUseBy:     v.InUseBy,
		CreatedUnix: v.CreatedUnix,
	}
}

func volumeListResultMessage(requestID string, volumes []docker.VolumeSummary) *agentv1.AgentMessage {
	out := make([]*agentv1.VolumeSummary, len(volumes))
	for i, v := range volumes {
		out[i] = volumeSummaryToProto(v)
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_VolumeListResult{
			VolumeListResult: &agentv1.VolumeListResult{RequestId: requestID, Volumes: out},
		},
	}
}

func volumeDetailMessage(requestID string, d docker.VolumeDetail) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_VolumeDetail{
			VolumeDetail: &agentv1.VolumeDetail{
				RequestId:    requestID,
				Found:        d.Found,
				ErrorMessage: d.ErrorMessage,
				Volume:       volumeSummaryToProto(d.Volume),
			},
		},
	}
}

func volumeOpResultMessage(requestID, name string, err error) *agentv1.AgentMessage {
	result := &agentv1.VolumeOpResult{
		RequestId: requestID,
		Success:   err == nil,
		Name:      name,
	}
	if err != nil {
		result.ErrorMessage = err.Error()
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_VolumeOpResult{VolumeOpResult: result},
	}
}
