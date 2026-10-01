/// One row of GET /api/volumes (or a single-volume detail from
/// GET /api/servers/{id}/volumes/{name}) — a Docker named volume, with the
/// owning server attached for the fleet-wide list. [orphaned] is derived
/// server-side from an empty [inUseBy].
class VolumeSummary {
  final String serverId;
  final String serverName;
  final String name;
  final String driver;
  final String mountpoint;
  final Map<String, String> labels;
  final int sizeBytes;
  final List<String> inUseBy;
  final bool orphaned;
  final int createdUnix;

  const VolumeSummary({
    required this.serverId,
    required this.serverName,
    required this.name,
    required this.driver,
    required this.mountpoint,
    this.labels = const {},
    this.sizeBytes = 0,
    this.inUseBy = const [],
    this.orphaned = true,
    this.createdUnix = 0,
  });

  DateTime? get createdAt => createdUnix > 0
      ? DateTime.fromMillisecondsSinceEpoch(createdUnix * 1000)
      : null;

  factory VolumeSummary.fromJson(Map<String, dynamic> json) {
    return VolumeSummary(
      serverId: json['serverId'] as String? ?? '',
      serverName: json['serverName'] as String? ?? '',
      name: json['name'] as String? ?? '',
      driver: json['driver'] as String? ?? '',
      mountpoint: json['mountpoint'] as String? ?? '',
      labels: (json['labels'] as Map<String, dynamic>? ?? {}).map(
        (k, v) => MapEntry(k, v as String),
      ),
      sizeBytes: (json['sizeBytes'] as num?)?.toInt() ?? 0,
      inUseBy: (json['inUseBy'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      orphaned: json['orphaned'] as bool? ?? true,
      createdUnix: (json['createdUnix'] as num?)?.toInt() ?? 0,
    );
  }
}

class VolumeFileEntry {
  final String name;
  final bool isDirectory;
  final bool isSymlink;
  final int sizeBytes;

  const VolumeFileEntry({
    required this.name,
    required this.isDirectory,
    required this.isSymlink,
    required this.sizeBytes,
  });

  factory VolumeFileEntry.fromJson(Map<String, dynamic> json) =>
      VolumeFileEntry(
        name: json['name'] as String,
        isDirectory: json['isDirectory'] as bool? ?? false,
        isSymlink: json['isSymlink'] as bool? ?? false,
        sizeBytes: (json['sizeBytes'] as num?)?.toInt() ?? 0,
      );
}

/// Result of a volume create/remove command that completed its round trip
/// to the agent. A remove blocked by the in-use guard (see
/// internal/agent/docker/volumes.go's RemoveVolume) comes back as
/// success:false with [error] describing which containers are using it —
/// the caller offers to retry with force in that case, not a transport
/// error.
class VolumeOpResult {
  final bool success;
  final String? error;
  final String? name;

  const VolumeOpResult({required this.success, this.error, this.name});

  factory VolumeOpResult.fromJson(Map<String, dynamic> json) {
    return VolumeOpResult(
      success: json['success'] as bool? ?? false,
      error: json['error'] as String?,
      name: json['name'] as String?,
    );
  }
}

/// Result of GET /api/servers/{id}/ports/check.
class PortConflictResult {
  final bool conflict;
  final String? containerId;

  const PortConflictResult({required this.conflict, this.containerId});

  factory PortConflictResult.fromJson(Map<String, dynamic> json) {
    return PortConflictResult(
      conflict: json['conflict'] as bool? ?? false,
      containerId: json['containerId'] as String?,
    );
  }
}
