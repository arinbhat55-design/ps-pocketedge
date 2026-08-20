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

class ContainerInfo {
  final String containerId;
  final String name;
  final String state;
  final String? deploymentId;

  const ContainerInfo({
    required this.containerId,
    required this.name,
    required this.state,
    required this.deploymentId,
  });

  factory ContainerInfo.fromJson(Map<String, dynamic> json) {
    return ContainerInfo(
      containerId: json['containerId'] as String,
      name: json['name'] as String,
      state: json['state'] as String,
      deploymentId: json['deploymentId'] as String?,
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
  final DateTime updatedAt;

  const ServerUpdate({
    required this.resources,
    required this.containers,
    required this.updatedAt,
  });

  factory ServerUpdate.fromJson(Map<String, dynamic> json) {
    return ServerUpdate(
      resources:
          ResourceSnapshot.fromJson(json['resources'] as Map<String, dynamic>),
      containers: (json['containers'] as List<dynamic>)
          .map((e) => ContainerInfo.fromJson(e as Map<String, dynamic>))
          .toList(),
      updatedAt: DateTime.parse(json['updatedAt'] as String),
    );
  }
}
