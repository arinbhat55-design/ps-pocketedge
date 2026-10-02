class ResourceSnapshot {
  final double cpuPercent;
  final double memPercent;
  final double diskPercent;
  final int totalMemoryBytes;
  final int totalDiskBytes;
  final int? usedMemoryBytes;
  final int? usedDiskBytes;

  const ResourceSnapshot({
    required this.cpuPercent,
    required this.memPercent,
    required this.diskPercent,
    this.totalMemoryBytes = 0,
    this.totalDiskBytes = 0,
    this.usedMemoryBytes,
    this.usedDiskBytes,
  });

  factory ResourceSnapshot.fromJson(Map<String, dynamic> json) {
    return ResourceSnapshot(
      cpuPercent: (json['cpuPercent'] as num).toDouble(),
      memPercent: (json['memPercent'] as num).toDouble(),
      diskPercent: (json['diskPercent'] as num).toDouble(),
      totalMemoryBytes: (json['totalMemoryBytes'] as num?)?.toInt() ?? 0,
      totalDiskBytes: (json['totalDiskBytes'] as num?)?.toInt() ?? 0,
      usedMemoryBytes: (json['usedMemoryBytes'] as num?)?.toInt(),
      usedDiskBytes: (json['usedDiskBytes'] as num?)?.toInt(),
    );
  }
}

class Server {
  final String id;
  final String name;
  final String hostname;
  final String os;
  final String arch;
  final String agentVersion;
  final String status;
  final DateTime? lastHeartbeatAt;
  final ResourceSnapshot? lastResources;
  final DateTime createdAt;

  const Server({
    required this.id,
    required this.name,
    required this.hostname,
    required this.os,
    required this.arch,
    required this.agentVersion,
    required this.status,
    required this.lastHeartbeatAt,
    required this.lastResources,
    required this.createdAt,
  });

  factory Server.fromJson(Map<String, dynamic> json) {
    return Server(
      id: json['id'] as String,
      name: json['name'] as String,
      hostname: json['hostname'] as String,
      os: json['os'] as String,
      arch: json['arch'] as String,
      agentVersion: json['agentVersion'] as String,
      status: json['status'] as String,
      lastHeartbeatAt: json['lastHeartbeatAt'] == null
          ? null
          : DateTime.parse(json['lastHeartbeatAt'] as String),
      lastResources: json['lastResources'] == null
          ? null
          : ResourceSnapshot.fromJson(
              json['lastResources'] as Map<String, dynamic>,
            ),
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}
