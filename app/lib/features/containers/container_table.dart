import 'package:flutter/material.dart';

import '../../models/container.dart';
import '../../theme/app_theme.dart';
import '../../widgets/status_pill.dart';

/// What a row shows for one container: its status pill, a line of detail
/// ("Up 3 hours (healthy)", or "Was running · 22 d ago" when the server is
/// disconnected), and whether its server can act on it right now.
typedef ContainerRowView = ({
  StatusLabel status,
  String? detail,
  bool reachable,
});

/// One group of rows. A single group with an empty [name] renders without
/// a group header (the ungrouped list).
typedef ContainerGroup = ({String name, List<FleetContainer> containers});

/// Actions the table can ask for; the screen runs them.
enum ContainerRowAction {
  start,
  stop,
  pause,
  resume,
  restart,
  remove,
  details,
  logs,
  terminal,
}

/// Desktop container table, laid out like Docker Desktop's: checkbox,
/// name, status, image, server, ports and ID, with the Actions column
/// pinned to the right edge so it's always visible.
///
/// Nothing scrolls sideways. On narrower windows lower-priority columns
/// drop out (ID, then Server, then Ports) and long values truncate with a
/// tooltip, so the actions never move off-screen. With grouping on, each
/// group is an expandable header row with its containers indented under
/// it.
class ContainerTable extends StatefulWidget {
  final List<ContainerGroup> groups;
  final Set<String> selectedKeys;
  final String Function(FleetContainer) keyOf;
  final ContainerRowView Function(FleetContainer) viewOf;
  final void Function(FleetContainer) onToggleSelected;
  final void Function(List<FleetContainer>, bool select) onSetSelected;
  final void Function(FleetContainer) onOpen;
  final void Function(FleetContainer, ContainerRowAction) onAction;

  const ContainerTable({
    super.key,
    required this.groups,
    required this.selectedKeys,
    required this.keyOf,
    required this.viewOf,
    required this.onToggleSelected,
    required this.onSetSelected,
    required this.onOpen,
    required this.onAction,
  });

  @override
  State<ContainerTable> createState() => _ContainerTableState();
}

// Fixed column widths; the rest share what's left by flex.
const double _checkboxW = 48;
const double _statusW = 140;
const double _idW = 124;
const double _actionsW = 212;
const double _groupIndent = 24;

class _Columns {
  final bool ports;
  final bool server;
  final bool id;

  const _Columns({required this.ports, required this.server, required this.id});

  factory _Columns.forWidth(double w) =>
      _Columns(ports: w >= 860, server: w >= 980, id: w >= 1140);
}

sealed class _Entry {}

class _HeaderEntry extends _Entry {
  final ContainerGroup group;
  _HeaderEntry(this.group);
}

class _RowEntry extends _Entry {
  final FleetContainer container;
  final bool indented;
  _RowEntry(this.container, {required this.indented});
}

class _ContainerTableState extends State<ContainerTable> {
  final Set<String> _collapsed = {};

  bool get _grouped =>
      widget.groups.length > 1 ||
      (widget.groups.length == 1 && widget.groups.first.name.isNotEmpty);

  List<FleetContainer> get _all => [
    for (final g in widget.groups) ...g.containers,
  ];

  List<_Entry> _entries() {
    if (!_grouped) {
      return [for (final c in _all) _RowEntry(c, indented: false)];
    }
    return [
      for (final g in widget.groups) ...[
        _HeaderEntry(g),
        if (!_collapsed.contains(g.name))
          for (final c in g.containers) _RowEntry(c, indented: true),
      ],
    ];
  }

  bool? _selectionState(List<FleetContainer> list) {
    if (list.isEmpty) return false;
    final n = list.where((c) => widget.selectedKeys.contains(widget.keyOf(c)));
    final count = n.length;
    if (count == 0) return false;
    if (count == list.length) return true;
    return null;
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final cols = _Columns.forWidth(constraints.maxWidth);
        final entries = _entries();
        final all = _all;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _HeaderRow(
              cols: cols,
              selection: _selectionState(all),
              onSelectAll: (v) => widget.onSetSelected(all, v),
            ),
            const Divider(),
            Expanded(
              child: ListView.separated(
                itemCount: entries.length,
                separatorBuilder: (_, _) => const Divider(),
                itemBuilder: (context, i) => switch (entries[i]) {
                  // Keyed by identity: without keys, regrouping or
                  // re-sorting reuses a row's button state for whichever
                  // container lands in that position.
                  _HeaderEntry(:final group) => _GroupRow(
                    key: ValueKey('group:${group.name}'),
                    group: group,
                    cols: cols,
                    expanded: !_collapsed.contains(group.name),
                    selection: _selectionState(group.containers),
                    viewOf: widget.viewOf,
                    onToggleExpanded: () => setState(() {
                      if (!_collapsed.remove(group.name)) {
                        _collapsed.add(group.name);
                      }
                    }),
                    onSelect: (v) => widget.onSetSelected(group.containers, v),
                  ),
                  _RowEntry(:final container, :final indented) => _DataRow(
                    key: ValueKey(widget.keyOf(container)),
                    container: container,
                    cols: cols,
                    indented: indented,
                    selected: widget.selectedKeys.contains(
                      widget.keyOf(container),
                    ),
                    view: widget.viewOf(container),
                    onToggleSelected: () => widget.onToggleSelected(container),
                    onOpen: () => widget.onOpen(container),
                    onAction: (a) => widget.onAction(container, a),
                  ),
                },
              ),
            ),
            const Divider(),
            Padding(
              padding: const EdgeInsets.symmetric(
                horizontal: Space.lg,
                vertical: Space.sm,
              ),
              child: Text(
                'Showing ${all.length} ${all.length == 1 ? 'item' : 'items'}',
                textAlign: TextAlign.end,
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
          ],
        );
      },
    );
  }
}

/// Lays out one row's cells in the shared column grid. [actions] sits in
/// the pinned right-hand column, separated by a vertical rule.
class _Cells extends StatelessWidget {
  final _Columns cols;
  final Widget checkbox;
  final Widget name;
  final Widget status;
  final Widget image;
  final Widget server;
  final Widget ports;
  final Widget id;
  final Widget actions;
  final double height;

  const _Cells({
    required this.cols,
    required this.checkbox,
    required this.name,
    required this.status,
    required this.image,
    required this.server,
    required this.ports,
    required this.id,
    required this.actions,
    required this.height,
  });

  @override
  Widget build(BuildContext context) {
    Widget flex(int f, Widget child) => Expanded(
      flex: f,
      child: Padding(
        padding: const EdgeInsets.only(right: Space.md),
        child: child,
      ),
    );
    Widget fixed(double w, Widget child) => SizedBox(width: w, child: child);

    return SizedBox(
      height: height,
      child: Row(
        children: [
          fixed(_checkboxW, Center(child: checkbox)),
          flex(4, name),
          fixed(
            _statusW,
            Align(alignment: Alignment.centerLeft, child: status),
          ),
          flex(3, image),
          if (cols.server) flex(2, server),
          if (cols.ports) flex(2, ports),
          if (cols.id) fixed(_idW, id),
          Container(
            width: _actionsW,
            height: height,
            decoration: BoxDecoration(
              border: Border(
                left: BorderSide(
                  color: Theme.of(context).colorScheme.outlineVariant,
                ),
              ),
            ),
            padding: const EdgeInsets.symmetric(horizontal: Space.sm),
            alignment: Alignment.centerLeft,
            child: actions,
          ),
        ],
      ),
    );
  }
}

class _HeaderRow extends StatelessWidget {
  final _Columns cols;
  final bool? selection;
  final ValueChanged<bool> onSelectAll;

  const _HeaderRow({
    required this.cols,
    required this.selection,
    required this.onSelectAll,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final style = theme.textTheme.labelMedium?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
      fontWeight: FontWeight.w600,
      letterSpacing: 0.4,
    );
    Widget h(String t) =>
        Text(t, style: style, overflow: TextOverflow.ellipsis);
    return ColoredBox(
      color: theme.colorScheme.surfaceContainerLow,
      child: _Cells(
        cols: cols,
        height: 44,
        checkbox: Checkbox(
          tristate: true,
          value: selection,
          onChanged: (_) => onSelectAll(selection != true),
        ),
        name: h('NAME'),
        status: h('STATUS'),
        image: h('IMAGE'),
        server: h('SERVER'),
        ports: h('PORT(S)'),
        id: h('CONTAINER ID'),
        actions: h('ACTIONS'),
      ),
    );
  }
}

class _GroupRow extends StatelessWidget {
  final ContainerGroup group;
  final _Columns cols;
  final bool expanded;
  final bool? selection;
  final ContainerRowView Function(FleetContainer) viewOf;
  final VoidCallback onToggleExpanded;
  final ValueChanged<bool> onSelect;

  const _GroupRow({
    super.key,
    required this.group,
    required this.cols,
    required this.expanded,
    required this.selection,
    required this.viewOf,
    required this.onToggleExpanded,
    required this.onSelect,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final total = group.containers.length;
    final running = group.containers
        .where((c) => viewOf(c).status.label == 'Running')
        .length;
    final StatusLabel summary = running == total && total > 0
        ? (label: 'Running', tone: StatusTone.healthy)
        : running == 0
        ? (label: 'Not running', tone: StatusTone.neutral)
        : (label: '$running/$total running', tone: StatusTone.warning);

    return InkWell(
      onTap: onToggleExpanded,
      child: _Cells(
        cols: cols,
        height: 48,
        checkbox: Checkbox(
          tristate: true,
          value: selection,
          onChanged: (_) => onSelect(selection != true),
        ),
        name: Row(
          children: [
            Icon(
              expanded ? Icons.expand_more : Icons.chevron_right,
              size: 20,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(width: Space.xs),
            Flexible(
              child: Text(
                group.name,
                style: theme.textTheme.titleSmall,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            const SizedBox(width: Space.sm),
            Text('$total', style: theme.textTheme.bodySmall),
          ],
        ),
        status: StatusPill.of(summary),
        image: const SizedBox.shrink(),
        server: const SizedBox.shrink(),
        ports: const SizedBox.shrink(),
        id: const SizedBox.shrink(),
        actions: const SizedBox.shrink(),
      ),
    );
  }
}

class _DataRow extends StatelessWidget {
  final FleetContainer container;
  final _Columns cols;
  final bool indented;
  final bool selected;
  final ContainerRowView view;
  final VoidCallback onToggleSelected;
  final VoidCallback onOpen;
  final ValueChanged<ContainerRowAction> onAction;

  const _DataRow({
    super.key,
    required this.container,
    required this.cols,
    required this.indented,
    required this.selected,
    required this.view,
    required this.onToggleSelected,
    required this.onOpen,
    required this.onAction,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final info = container.container;
    final id = info.containerId;
    final shortId = id.length > 12 ? id.substring(0, 12) : id;
    final portLabels = [
      for (final p in info.ports)
        p.publicPort != 0
            ? '${p.publicPort}:${p.privatePort}'
            : '${p.privatePort}/${p.type.isEmpty ? 'tcp' : p.type}',
    ];

    return Material(
      color: selected
          ? theme.colorScheme.primaryContainer.withValues(alpha: 0.45)
          : Colors.transparent,
      child: InkWell(
        onTap: onOpen,
        child: _Cells(
          cols: cols,
          height: 56,
          checkbox: Checkbox(
            value: selected,
            onChanged: (_) => onToggleSelected(),
          ),
          name: Padding(
            padding: EdgeInsets.only(left: indented ? _groupIndent : 0),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Tooltip(
                  message: info.name,
                  waitDuration: const Duration(milliseconds: 600),
                  child: Text(
                    info.name,
                    style: theme.textTheme.titleSmall,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (view.detail != null)
                  Text(
                    view.detail!,
                    style: theme.textTheme.bodySmall,
                    overflow: TextOverflow.ellipsis,
                  ),
              ],
            ),
          ),
          status: StatusPill.of(view.status),
          image: _Truncated(info.image ?? '—'),
          server: _Truncated(container.serverName),
          ports: _PortsCell(labels: portLabels),
          id: Tooltip(
            message: id,
            waitDuration: const Duration(milliseconds: 600),
            child: Text(
              shortId,
              style: AppText.mono(context),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          actions: _Actions(
            state: info.state,
            enabled: view.reachable,
            onAction: onAction,
          ),
        ),
      ),
    );
  }
}

class _Truncated extends StatelessWidget {
  final String text;

  const _Truncated(this.text);

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: text,
      waitDuration: const Duration(milliseconds: 600),
      child: Text(
        text,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: Theme.of(context).textTheme.bodyMedium,
      ),
    );
  }
}

/// First port, plus "+N more" with the full list in a tooltip.
class _PortsCell extends StatelessWidget {
  final List<String> labels;

  const _PortsCell({required this.labels});

  @override
  Widget build(BuildContext context) {
    if (labels.isEmpty) {
      return Text('—', style: Theme.of(context).textTheme.bodySmall);
    }
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          labels.first,
          style: AppText.mono(
            context,
            size: 12.5,
          ).copyWith(color: Theme.of(context).colorScheme.onSurface),
          overflow: TextOverflow.ellipsis,
        ),
        if (labels.length > 1)
          Tooltip(
            message: labels.join('\n'),
            child: Text(
              '+${labels.length - 1} more',
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: AppColors.info,
                decoration: TextDecoration.underline,
                decorationColor: AppColors.info,
              ),
            ),
          ),
      ],
    );
  }
}

/// The pinned action buttons. Each has its own color so the row reads at a
/// glance: start/stop in the primary teal, pause/resume in amber, restart
/// in blue, delete in red; logs/terminal/details live under ⋮.
class _Actions extends StatelessWidget {
  final String state;
  final bool enabled;
  final ValueChanged<ContainerRowAction> onAction;

  const _Actions({
    required this.state,
    required this.enabled,
    required this.onAction,
  });

  @override
  Widget build(BuildContext context) {
    final running = state == 'running';
    final paused = state == 'paused';
    VoidCallback? on(ContainerRowAction a, {bool when = true}) =>
        enabled && when ? () => onAction(a) : null;

    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        // Start/Stop reflects whether the container is up at all (a paused
        // container is still up); Pause/Resume sits next to it.
        running || paused
            ? _ActionButton(
                icon: Icons.stop_rounded,
                color: AppColors.teal,
                filled: true,
                tooltip: 'Stop',
                onPressed: on(ContainerRowAction.stop),
              )
            : _ActionButton(
                icon: Icons.play_arrow_rounded,
                color: AppColors.teal,
                tooltip: 'Start',
                onPressed: on(ContainerRowAction.start),
              ),
        _ActionButton(
          icon: paused ? Icons.play_circle_outline : Icons.pause_rounded,
          color: AppColors.warning,
          tooltip: paused ? 'Resume' : 'Pause',
          onPressed: on(
            paused ? ContainerRowAction.resume : ContainerRowAction.pause,
            when: running || paused,
          ),
        ),
        _ActionButton(
          icon: Icons.restart_alt_rounded,
          color: AppColors.info,
          tooltip: 'Restart',
          onPressed: on(ContainerRowAction.restart, when: running),
        ),
        SizedBox.square(
          dimension: _actionSize,
          child: PopupMenuButton<ContainerRowAction>(
            tooltip: 'More actions',
            icon: const Icon(Icons.more_vert, size: 20),
            padding: EdgeInsets.zero,
            onSelected: onAction,
            itemBuilder: (context) => const [
              PopupMenuItem(
                value: ContainerRowAction.details,
                child: ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.info_outline),
                  title: Text('Details'),
                ),
              ),
              PopupMenuItem(
                value: ContainerRowAction.logs,
                child: ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.article_outlined),
                  title: Text('Logs'),
                ),
              ),
              PopupMenuItem(
                value: ContainerRowAction.terminal,
                child: ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.terminal),
                  title: Text('Terminal'),
                ),
              ),
            ],
          ),
        ),
        Container(
          width: 1,
          height: 20,
          margin: const EdgeInsets.symmetric(horizontal: Space.xs),
          color: Theme.of(context).colorScheme.outlineVariant,
        ),
        _ActionButton(
          icon: Icons.delete_outline_rounded,
          color: AppColors.failed,
          tooltip: 'Remove',
          onPressed: on(ContainerRowAction.remove),
        ),
      ],
    );
  }
}

const double _actionSize = 34;

/// A 34 px icon button in its own color, with a matching tinted hover.
/// [filled] gives the "currently running → Stop" button a solid chip so
/// the row's live state is obvious.
class _ActionButton extends StatelessWidget {
  final IconData icon;
  final Color color;
  final String tooltip;
  final VoidCallback? onPressed;
  final bool filled;

  const _ActionButton({
    required this.icon,
    required this.color,
    required this.tooltip,
    required this.onPressed,
    this.filled = false,
  });

  @override
  Widget build(BuildContext context) {
    final enabled = onPressed != null;
    final c = enabled ? color : AppColors.textMuted.withValues(alpha: 0.5);
    return IconButton(
      tooltip: tooltip,
      onPressed: onPressed,
      iconSize: 20,
      padding: EdgeInsets.zero,
      style: IconButton.styleFrom(
        fixedSize: const Size.square(_actionSize),
        minimumSize: const Size.square(_actionSize),
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        foregroundColor: filled && enabled ? AppColors.onTeal : c,
        disabledForegroundColor: c,
        backgroundColor: filled && enabled ? color : Colors.transparent,
        hoverColor: color.withValues(alpha: 0.14),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(Radii.sm),
        ),
      ),
      icon: Icon(icon),
    );
  }
}
