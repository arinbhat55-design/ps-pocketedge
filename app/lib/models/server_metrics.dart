import 'server.dart';

class MetricSample {
  final DateTime recordedAt;
  final double cpuPercent;
  final double memPercent;
  final double diskPercent;

  const MetricSample({
    required this.recordedAt,
    required this.cpuPercent,
    required this.memPercent,
    required this.diskPercent,
  });

  factory MetricSample.fromJson(Map<String, dynamic> json) {
    return MetricSample(
      recordedAt: DateTime.parse(json['recordedAt'] as String),
      cpuPercent: (json['cpuPercent'] as num).toDouble(),
      memPercent: (json['memPercent'] as num).toDouble(),
      diskPercent: (json['diskPercent'] as num).toDouble(),
    );
  }
}

class ContainerPort {
  final String ip;
  final int privatePort;
  final int publicPort;
  final String type;

  const ContainerPort({
    required this.ip,
    required this.privatePort,
    required this.publicPort,
    required this.type,
  });

  factory ContainerPort.fromJson(Map<String, dynamic> json) {
    return ContainerPort(
      ip: json['ip'] as String? ?? '',
      privatePort: (json['privatePort'] as num?)?.toInt() ?? 0,
      publicPort: (json['publicPort'] as num?)?.toInt() ?? 0,
      type: json['type'] as String? ?? '',
    );
  }
}

class ContainerNetworkInfo {
  final String name;
  final String ipAddress;

  const ContainerNetworkInfo({required this.name, required this.ipAddress});

  factory ContainerNetworkInfo.fromJson(Map<String, dynamic> json) {
    return ContainerNetworkInfo(
      name: json['name'] as String? ?? '',
      ipAddress: json['ipAddress'] as String? ?? '',
    );
  }
}

class ContainerMountInfo {
  final String type;
  final String name;
  final String source;
  final String destination;
  final bool readWrite;

  const ContainerMountInfo({
    required this.type,
    required this.name,
    required this.source,
    required this.destination,
    required this.readWrite,
  });

  factory ContainerMountInfo.fromJson(Map<String, dynamic> json) {
    return ContainerMountInfo(
      type: json['type'] as String? ?? '',
      name: json['name'] as String? ?? '',
      source: json['source'] as String? ?? '',
      destination: json['destination'] as String? ?? '',
      readWrite: json['readWrite'] as bool? ?? false,
    );
  }
}

/// One point-in-time resource-usage sample for a single container — from
/// either GET .../containers/{id}/metrics (history) or a live server-stream
/// update (see ServerUpdate.containerStats below). Mirrors
/// store.ContainerResourceUsage on the control plane.
class ContainerResourceUsage {
  final String containerId;
  final DateTime recordedAt;
  final double cpuPercent;
  final int memUsageBytes;
  final int memLimitBytes;
  final double memPercent;
  final int netRxBytes;
  final int netTxBytes;
  final int blockReadBytes;
  final int blockWriteBytes;
  final int pids;

  const ContainerResourceUsage({
    required this.containerId,
    required this.recordedAt,
    required this.cpuPercent,
    required this.memUsageBytes,
    required this.memLimitBytes,
    required this.memPercent,
    required this.netRxBytes,
    required this.netTxBytes,
    required this.blockReadBytes,
    required this.blockWriteBytes,
    required this.pids,
  });

  factory ContainerResourceUsage.fromJson(Map<String, dynamic> json) {
    return ContainerResourceUsage(
      containerId: json['containerId'] as String,
      recordedAt: DateTime.parse(json['recordedAt'] as String),
      cpuPercent: (json['cpuPercent'] as num).toDouble(),
      memUsageBytes: (json['memUsageBytes'] as num).toInt(),
      memLimitBytes: (json['memLimitBytes'] as num).toInt(),
      memPercent: (json['memPercent'] as num).toDouble(),
      netRxBytes: (json['netRxBytes'] as num).toInt(),
      netTxBytes: (json['netTxBytes'] as num).toInt(),
      blockReadBytes: (json['blockReadBytes'] as num).toInt(),
      blockWriteBytes: (json['blockWriteBytes'] as num).toInt(),
      pids: (json['pids'] as num).toInt(),
    );
  }
}

/// Cheap per-container fields, populated from the agent's ContainerList
/// call and refreshed on every heartbeat. Fields that require the agent to
/// run ContainerInspect (env vars, restart policy, health) live separately
/// in [ContainerDetail], fetched on demand — see models/container.dart.
class ContainerInfo {
  final String containerId;
  final String name;
  final String state;
  final String? deploymentId;
  final String? image;
  final String? imageId;
  final DateTime? createdAt;
  final String? status;
  final List<ContainerPort> ports;
  final List<ContainerNetworkInfo> networks;
  final List<ContainerMountInfo> mounts;

  const ContainerInfo({
    required this.containerId,
    required this.name,
    required this.state,
    required this.deploymentId,
    this.image,
    this.imageId,
    this.createdAt,
    this.status,
    this.ports = const [],
    this.networks = const [],
    this.mounts = const [],
  });

  factory ContainerInfo.fromJson(Map<String, dynamic> json) {
    return ContainerInfo(
      containerId: json['containerId'] as String,
      name: json['name'] as String,
      state: json['state'] as String,
      deploymentId: json['deploymentId'] as String?,
      image: json['image'] as String?,
      imageId: json['imageId'] as String?,
      createdAt: json['createdAt'] == null
          ? null
          : DateTime.parse(json['createdAt'] as String),
      status: json['status'] as String?,
      ports: (json['ports'] as List<dynamic>? ?? [])
          .map((e) => ContainerPort.fromJson(e as Map<String, dynamic>))
          .toList(),
      networks: (json['networks'] as List<dynamic>? ?? [])
          .map((e) => ContainerNetworkInfo.fromJson(e as Map<String, dynamic>))
          .toList(),
      mounts: (json['mounts'] as List<dynamic>? ?? [])
          .map((e) => ContainerMountInfo.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// Full detail for one server: the base [Server] fields plus its current
/// container inventory — GET /api/servers/{id}'s response shape.
class ServerDetail {
  final Server server;
  final List<ContainerInfo> containers;

  const ServerDetail({required this.server, required this.containers});

  factory ServerDetail.fromJson(Map<String, dynamic> json) {
    return ServerDetail(
      server: Server.fromJson(json),
      containers: (json['containers'] as List<dynamic>)
          .map((e) => ContainerInfo.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// Live push payload from a server's status stream: current resources and
/// container inventory, refreshed on every heartbeat.
class ServerUpdate {
  final ResourceSnapshot resources;
  final List<ContainerInfo> containers;
  // Empty on a tick that didn't sample per-container resource usage (see
  // the control plane's grpcserver/session.go) — treat that as "no new
  // sample", not "zero usage".
  final List<ContainerResourceUsage> containerStats;
  final DateTime updatedAt;

  const ServerUpdate({
    required this.resources,
    required this.containers,
    this.containerStats = const [],
    required this.updatedAt,
  });

  factory ServerUpdate.fromJson(Map<String, dynamic> json) {
    return ServerUpdate(
      resources: ResourceSnapshot.fromJson(
        json['resources'] as Map<String, dynamic>,
      ),
      containers: (json['containers'] as List<dynamic>)
          .map((e) => ContainerInfo.fromJson(e as Map<String, dynamic>))
          .toList(),
      containerStats: (json['containerStats'] as List<dynamic>? ?? [])
          .map((e) => ContainerResourceUsage.fromJson(e as Map<String, dynamic>))
          .toList(),
      updatedAt: DateTime.parse(json['updatedAt'] as String),
    );
  }
}
