/// One row of GET /api/images — an image present on a server, with the
/// owning server attached so a fleet-wide (no ?serverId filter) list can
/// show where each image lives.
class ImageSummary {
  final String serverId;
  final String serverName;
  final String id;
  final List<String> repoTags;
  final List<String> repoDigests;
  final int sizeBytes;
  final int createdUnix;
  final bool dangling;
  final int containersCount;

  const ImageSummary({
    required this.serverId,
    required this.serverName,
    required this.id,
    required this.repoTags,
    required this.repoDigests,
    required this.sizeBytes,
    required this.createdUnix,
    required this.dangling,
    required this.containersCount,
  });

  /// A short id (matches `docker images`' 12-char short id), for display
  /// where the full sha256 id would be too wide. Real Docker image ids are
  /// always a full 64-char digest, but this clamps to the string's actual
  /// length regardless, so a shorter/malformed id can't crash the list
  /// (RangeError) instead of just showing a shorter-than-usual id.
  String get shortId {
    final stripped = id.startsWith('sha256:') ? id.substring(7) : id;
    return stripped.length <= 12 ? stripped : stripped.substring(0, 12);
  }

  DateTime get createdAt =>
      DateTime.fromMillisecondsSinceEpoch(createdUnix * 1000);

  factory ImageSummary.fromJson(Map<String, dynamic> json) {
    return ImageSummary(
      serverId: json['serverId'] as String,
      serverName: json['serverName'] as String,
      id: json['id'] as String,
      repoTags: (json['repoTags'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      repoDigests: (json['repoDigests'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      sizeBytes: (json['sizeBytes'] as num?)?.toInt() ?? 0,
      createdUnix: (json['createdUnix'] as num?)?.toInt() ?? 0,
      dangling: json['dangling'] as bool? ?? false,
      containersCount: (json['containersCount'] as num?)?.toInt() ?? 0,
    );
  }
}

/// One layer of an image's root filesystem.
class ImageLayer {
  final String digest;
  final int sizeBytes;

  const ImageLayer({required this.digest, required this.sizeBytes});

  factory ImageLayer.fromJson(Map<String, dynamic> json) {
    return ImageLayer(
      digest: json['digest'] as String? ?? '',
      sizeBytes: (json['sizeBytes'] as num?)?.toInt() ?? 0,
    );
  }
}

/// Expensive per-image fields, fetched on demand from
/// GET /api/servers/{id}/images/{imageId}/inspect.
class ImageDetail {
  final String id;
  final List<String> repoTags;
  final List<String> repoDigests;
  final int sizeBytes;
  final int createdUnix;
  final String architecture;
  final String os;
  final List<ImageLayer> layers;
  final List<String> env;
  final Map<String, String> labels;

  const ImageDetail({
    required this.id,
    required this.repoTags,
    required this.repoDigests,
    required this.sizeBytes,
    required this.createdUnix,
    required this.architecture,
    required this.os,
    required this.layers,
    required this.env,
    required this.labels,
  });

  factory ImageDetail.fromJson(Map<String, dynamic> json) {
    return ImageDetail(
      id: json['id'] as String? ?? '',
      repoTags: (json['repoTags'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      repoDigests: (json['repoDigests'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      sizeBytes: (json['sizeBytes'] as num?)?.toInt() ?? 0,
      createdUnix: (json['createdUnix'] as num?)?.toInt() ?? 0,
      architecture: json['architecture'] as String? ?? '',
      os: json['os'] as String? ?? '',
      layers: (json['layers'] as List<dynamic>? ?? [])
          .map((e) => ImageLayer.fromJson(e as Map<String, dynamic>))
          .toList(),
      env: (json['env'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      labels: (json['labels'] as Map<String, dynamic>? ?? {}).map(
        (k, v) => MapEntry(k, v as String),
      ),
    );
  }
}

/// Result of an image pull/remove/prune command that completed its round
/// trip to the agent.
class ImageOpResult {
  final bool success;
  final String? error;
  final String? imageId;
  final int reclaimedBytes;

  const ImageOpResult({
    required this.success,
    this.error,
    this.imageId,
    this.reclaimedBytes = 0,
  });

  factory ImageOpResult.fromJson(Map<String, dynamic> json) {
    return ImageOpResult(
      success: json['success'] as bool? ?? false,
      error: json['error'] as String?,
      imageId: json['imageId'] as String?,
      reclaimedBytes: (json['reclaimedBytes'] as num?)?.toInt() ?? 0,
    );
  }
}

/// Whether a newer image is available for a given tag, from
/// GET /api/images/newer.
class ImageUpdateStatus {
  final bool upToDate;
  final String? localDigest;
  final String? remoteDigest;

  const ImageUpdateStatus({
    required this.upToDate,
    this.localDigest,
    this.remoteDigest,
  });

  factory ImageUpdateStatus.fromJson(Map<String, dynamic> json) {
    return ImageUpdateStatus(
      upToDate: json['upToDate'] as bool? ?? true,
      localDigest: json['localDigest'] as String?,
      remoteDigest: json['remoteDigest'] as String?,
    );
  }
}

/// An admin-configured private registry's credentials (password never
/// round-trips back from the API).
class Registry {
  final String id;
  final String name;
  final String url;
  final String username;

  const Registry({
    required this.id,
    required this.name,
    required this.url,
    required this.username,
  });

  factory Registry.fromJson(Map<String, dynamic> json) {
    return Registry(
      id: json['id'] as String,
      name: json['name'] as String,
      url: json['url'] as String,
      username: json['username'] as String? ?? '',
    );
  }
}

/// One hit from a Docker Hub or private-registry search.
class RegistrySearchResult {
  final String name;
  final String description;
  final int starCount;
  final bool official;

  const RegistrySearchResult({
    required this.name,
    this.description = '',
    this.starCount = 0,
    this.official = false,
  });

  factory RegistrySearchResult.fromJson(Map<String, dynamic> json) {
    return RegistrySearchResult(
      name: json['name'] as String? ?? '',
      description: json['description'] as String? ?? '',
      starCount: (json['starCount'] as num?)?.toInt() ?? 0,
      official: json['official'] as bool? ?? false,
    );
  }
}

/// One approved-image allowlist pattern.
class ApprovedImage {
  final String id;
  final String pattern;
  final String note;

  const ApprovedImage({
    required this.id,
    required this.pattern,
    this.note = '',
  });

  factory ApprovedImage.fromJson(Map<String, dynamic> json) {
    return ApprovedImage(
      id: json['id'] as String,
      pattern: json['pattern'] as String,
      note: json['note'] as String? ?? '',
    );
  }
}

/// One recorded previous image for a container's rollback history.
class ImageRollbackEntry {
  final String id;
  final String previousImage;
  final DateTime capturedAt;

  const ImageRollbackEntry({
    required this.id,
    required this.previousImage,
    required this.capturedAt,
  });

  factory ImageRollbackEntry.fromJson(Map<String, dynamic> json) {
    return ImageRollbackEntry(
      id: json['id'] as String,
      previousImage: json['previousImage'] as String,
      capturedAt: DateTime.parse(json['capturedAt'] as String),
    );
  }
}

/// One summarized vulnerability from an image scan.
class Vulnerability {
  final String id;
  final String pkgName;
  final String severity;
  final String title;
  final String fixedVersion;

  const Vulnerability({
    required this.id,
    required this.pkgName,
    required this.severity,
    this.title = '',
    this.fixedVersion = '',
  });

  factory Vulnerability.fromJson(Map<String, dynamic> json) {
    return Vulnerability(
      id: json['id'] as String? ?? '',
      pkgName: json['pkgName'] as String? ?? '',
      severity: json['severity'] as String? ?? '',
      title: json['title'] as String? ?? '',
      fixedVersion: json['fixedVersion'] as String? ?? '',
    );
  }
}

/// A vulnerability scan result for one image reference.
class ScanResult {
  final int criticalCount;
  final int highCount;
  final int mediumCount;
  final int lowCount;
  final int unknownCount;
  final List<Vulnerability> vulnerabilities;

  const ScanResult({
    this.criticalCount = 0,
    this.highCount = 0,
    this.mediumCount = 0,
    this.lowCount = 0,
    this.unknownCount = 0,
    this.vulnerabilities = const [],
  });

  int get totalCount =>
      criticalCount + highCount + mediumCount + lowCount + unknownCount;

  factory ScanResult.fromJson(Map<String, dynamic> json) {
    return ScanResult(
      criticalCount: (json['criticalCount'] as num?)?.toInt() ?? 0,
      highCount: (json['highCount'] as num?)?.toInt() ?? 0,
      mediumCount: (json['mediumCount'] as num?)?.toInt() ?? 0,
      lowCount: (json['lowCount'] as num?)?.toInt() ?? 0,
      unknownCount: (json['unknownCount'] as num?)?.toInt() ?? 0,
      vulnerabilities: (json['vulnerabilities'] as List<dynamic>? ?? [])
          .map((e) => Vulnerability.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// Formats a byte count as a human-readable size (e.g. "128.4 MB"),
/// shared by the image list/detail screens.
String formatBytes(int bytes) {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var size = bytes.toDouble();
  var unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex++;
  }
  // A whole number (e.g. exactly 1024 bytes -> 1 KB) shouldn't show a
  // trailing ".0" — only a genuine fraction below the "show as an integer
  // past 10" threshold gets one decimal place.
  final isWhole = size == size.roundToDouble();
  final decimals = unitIndex == 0 || size >= 10 || isWhole ? 0 : 1;
  return '${size.toStringAsFixed(decimals)} ${units[unitIndex]}';
}
