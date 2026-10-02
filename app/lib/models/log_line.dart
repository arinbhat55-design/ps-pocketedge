/// One line from a container's combined stdout/stderr, as sent by both the
/// live log-stream WebSocket and the bounded log-fetch (download/AI
/// analysis) REST responses.
class LogLine {
  final String containerId;
  final DateTime? timestamp;
  final String stream; // "stdout" or "stderr"
  final String message;

  const LogLine({
    required this.containerId,
    this.timestamp,
    required this.stream,
    required this.message,
  });

  factory LogLine.fromJson(Map<String, dynamic> json) {
    final nanos = (json['timestampUnixNano'] as num?)?.toInt() ?? 0;
    return LogLine(
      containerId: json['containerId'] as String? ?? '',
      timestamp: nanos == 0
          ? null
          : DateTime.fromMicrosecondsSinceEpoch(nanos ~/ 1000),
      stream: json['stream'] as String? ?? 'stdout',
      message: json['message'] as String? ?? '',
    );
  }
}

/// One WebSocket message on the live log-stream endpoint: a batch of new
/// lines for one container, or a terminal "done" (stream ended/cancelled,
/// optionally carrying an error).
class LogChunk {
  final String containerId;
  final List<LogLine> lines;
  final bool done;
  final String? error;

  const LogChunk({
    required this.containerId,
    required this.lines,
    required this.done,
    this.error,
  });

  factory LogChunk.fromJson(Map<String, dynamic> json) {
    return LogChunk(
      containerId: json['containerId'] as String? ?? '',
      lines: (json['lines'] as List<dynamic>? ?? [])
          .map((e) => LogLine.fromJson(e as Map<String, dynamic>))
          .toList(),
      done: json['done'] as bool? ?? false,
      error: json['error'] as String?,
    );
  }
}

/// One Docker event for a container (start/stop/die/health_status/...),
/// from GET /api/servers/{id}/containers/{containerId}/events.
class ContainerEvent {
  final DateTime timestamp;
  final String type;
  final String action;
  final String containerId;
  final String containerName;

  const ContainerEvent({
    required this.timestamp,
    required this.type,
    required this.action,
    required this.containerId,
    required this.containerName,
  });

  factory ContainerEvent.fromJson(Map<String, dynamic> json) {
    final unix = (json['timestampUnix'] as num?)?.toInt() ?? 0;
    return ContainerEvent(
      timestamp: DateTime.fromMillisecondsSinceEpoch(unix * 1000),
      type: json['type'] as String? ?? '',
      action: json['action'] as String? ?? '',
      containerId: json['containerId'] as String? ?? '',
      containerName: json['containerName'] as String? ?? '',
    );
  }
}

/// AI-assisted log summary + root-cause result, from
/// POST /api/servers/{id}/containers/{containerId}/logs/analyze.
class LogAnalysis {
  final String summary;
  final String rootCause;
  final String recommendation;

  const LogAnalysis({
    required this.summary,
    required this.rootCause,
    required this.recommendation,
  });

  factory LogAnalysis.fromJson(Map<String, dynamic> json) {
    return LogAnalysis(
      summary: json['summary'] as String? ?? '',
      rootCause: json['rootCause'] as String? ?? '',
      recommendation: json['recommendation'] as String? ?? '',
    );
  }
}
