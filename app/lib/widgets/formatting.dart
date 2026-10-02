/// "just now", "45 s ago", "12 min ago", "3 h ago", "2 d ago".
String formatAgo(DateTime at, {DateTime? now}) {
  final d = (now ?? DateTime.now()).difference(at);
  if (d.inSeconds < 10) return 'just now';
  if (d.inMinutes < 1) return '${d.inSeconds} s ago';
  if (d.inHours < 1) return '${d.inMinutes} min ago';
  if (d.inDays < 1) return '${d.inHours} h ago';
  return '${d.inDays} d ago';
}

/// A time-range length as a short label: "15 min", "1 h", "24 h".
String formatRange(Duration range) =>
    range.inHours >= 1 ? '${range.inHours} h' : '${range.inMinutes} min';

/// The same range as a Go duration string, for `?since=` query params.
String goDuration(Duration range) =>
    range.inHours >= 1 ? '${range.inHours}h' : '${range.inMinutes}m';
