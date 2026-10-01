import 'package:flutter/material.dart';

import '../../models/deployment.dart' show formatTimestamp, shortCommit;
import '../../models/git_repository.dart';
import '../../widgets/formatting.dart';

/// "2026-10-01 09:15 (5 h ago)".
String formatWhen(DateTime t, {DateTime? now}) =>
    '${formatTimestamp(t)} (${formatAgo(t, now: now)})';

/// One side of a deployed-vs-latest comparison: "Deployed  abc1234 Fix
/// login", then when it was committed and (for the deployed side) rolled
/// out. [info] is null when the repository's history couldn't be read.
class CommitSummaryLine extends StatelessWidget {
  final String label;
  final String commit;
  final GitCommit? info;
  final DateTime? deployedAt;

  const CommitSummaryLine({
    super.key,
    required this.label,
    required this.commit,
    this.info,
    this.deployedAt,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final small = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
    );
    final info = this.info;
    final when = [
      if (info != null) 'committed ${formatWhen(info.date)}',
      if (deployedAt != null) 'deployed ${formatWhen(deployedAt!)}',
    ].join(' · ');
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 72,
            child: Text(label, style: theme.textTheme.labelMedium),
          ),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text.rich(
                  TextSpan(
                    children: [
                      TextSpan(
                        text: commit.isEmpty ? '—' : shortCommit(commit),
                        style: const TextStyle(fontFamily: 'monospace'),
                      ),
                      if (info != null && info.message.isNotEmpty)
                        TextSpan(text: '  ${info.message}'),
                    ],
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                if (when.isNotEmpty) Text(when, style: small),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// The commits on a branch that aren't deployed yet, newest first, each
/// with its author and date, under [title]. [more] adds a note that the
/// list was cut off.
class CommitsBehindList extends StatelessWidget {
  final List<GitCommit> commits;
  final bool more;
  final String title;

  const CommitsBehindList({
    super.key,
    required this.commits,
    this.more = false,
    this.title = 'Not deployed yet',
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final small = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: theme.textTheme.labelMedium),
        const SizedBox(height: 4),
        for (final c in commits)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  shortCommit(c.hash),
                  style: const TextStyle(fontFamily: 'monospace'),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        c.message.isEmpty ? '(no message)' : c.message,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      Text(
                        [
                          if (c.author.isNotEmpty) c.author,
                          formatWhen(c.date),
                        ].join(' · '),
                        style: small,
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        if (more)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text(
              '…and older commits not shown.',
              style: small?.copyWith(fontStyle: FontStyle.italic),
            ),
          ),
      ],
    );
  }
}
