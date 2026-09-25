import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/user.dart';
import '../../widgets/state_message.dart';
import '../containers/container_list_screen.dart';
import '../compose/docker_compose_screen.dart';
import '../databases/database_marketplace_screen.dart';
import '../images/image_list_screen.dart';
import '../networks/network_list_screen.dart';
import '../servers/server_list_screen.dart';
import '../users/change_password_dialog.dart';
import '../users/user_list_screen.dart';
import '../volumes/volume_list_screen.dart';

/// Post-login shell: lists the app's main modules (Servers, Containers,
/// ... and — admin only — Users), with the selected module's screen in an
/// [IndexedStack] so switching modules doesn't lose each one's
/// scroll/search state. Wider than [kCompactWidth] it's a persistent left
/// [NavigationRail]; on phones it's a bottom [NavigationBar] with a "More"
/// sheet for the remaining modules and account actions.
class AppShell extends StatefulWidget {
  final ApiClient apiClient;
  final VoidCallback onLogout;

  const AppShell({super.key, required this.apiClient, required this.onLogout});

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  int _selectedIndex = 0;
  late Future<AppUser> _meFuture;

  @override
  void initState() {
    super.initState();
    _meFuture = widget.apiClient.getMe();
  }

  Future<void> _openChangePassword() async {
    await showDialog<bool>(
      context: context,
      builder: (_) => ChangePasswordDialog(apiClient: widget.apiClient),
    );
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<AppUser>(
      future: _meFuture,
      builder: (context, snapshot) {
        if (snapshot.connectionState != ConnectionState.done) {
          return const Scaffold(
            body: Center(child: CircularProgressIndicator()),
          );
        }

        final isAdmin = snapshot.data?.isAdmin ?? false;
        final destinations = <_ModuleDestination>[
          _ModuleDestination(
            icon: Icons.dns_outlined,
            selectedIcon: Icons.dns,
            label: 'Servers',
            builder: (_) =>
                ServerListScreen(apiClient: widget.apiClient, isAdmin: isAdmin),
          ),
          _ModuleDestination(
            icon: Icons.view_in_ar_outlined,
            selectedIcon: Icons.view_in_ar,
            label: 'Container Management',
            shortLabel: 'Containers',
            builder: (_) => ContainerListScreen(apiClient: widget.apiClient),
          ),
          _ModuleDestination(
            icon: Icons.inventory_2_outlined,
            selectedIcon: Icons.inventory_2,
            label: 'Images',
            builder: (_) =>
                ImageListScreen(apiClient: widget.apiClient, isAdmin: isAdmin),
          ),
          _ModuleDestination(
            icon: Icons.hub_outlined,
            selectedIcon: Icons.hub,
            label: 'Networks',
            builder: (_) => NetworkListScreen(apiClient: widget.apiClient),
          ),
          _ModuleDestination(
            icon: Icons.storage_outlined,
            selectedIcon: Icons.storage,
            label: 'Volumes',
            builder: (_) => VolumeListScreen(apiClient: widget.apiClient),
          ),
          _ModuleDestination(
            icon: Icons.rocket_launch_outlined,
            selectedIcon: Icons.rocket_launch,
            label: 'Deployment Management',
            shortLabel: 'Deploy',
            builder: (_) => DockerComposeScreen(
              apiClient: widget.apiClient,
              isAdmin: isAdmin,
            ),
          ),
          _ModuleDestination(
            icon: Icons.storefront_outlined,
            selectedIcon: Icons.storefront,
            label: 'Databases',
            builder: (_) => DatabaseMarketplaceScreen(
              apiClient: widget.apiClient,
              isAdmin: isAdmin,
            ),
          ),
          if (isAdmin)
            _ModuleDestination(
              icon: Icons.people_outline,
              selectedIcon: Icons.people,
              label: 'Users',
              builder: (_) => UserListScreen(
                apiClient: widget.apiClient,
                currentUserId: snapshot.data!.id,
              ),
            ),
        ];

        final selectedIndex = _selectedIndex < destinations.length
            ? _selectedIndex
            : 0;
        final pages = IndexedStack(
          index: selectedIndex,
          children: [for (final d in destinations) d.builder(context)],
        );

        if (isCompactWidth(context)) {
          return _buildPhoneLayout(
            destinations: destinations,
            selectedIndex: selectedIndex,
            email: snapshot.data?.email,
            body: pages,
          );
        }

        final wide = MediaQuery.sizeOf(context).width > 800;
        return Scaffold(
          body: Row(
            children: [
              NavigationRail(
                extended: wide,
                selectedIndex: selectedIndex,
                onDestinationSelected: (i) =>
                    setState(() => _selectedIndex = i),
                labelType: wide
                    ? NavigationRailLabelType.none
                    : NavigationRailLabelType.all,
                leading: const SizedBox(height: 8),
                trailing: Expanded(
                  child: Align(
                    alignment: Alignment.bottomCenter,
                    child: Padding(
                      padding: const EdgeInsets.only(bottom: 12),
                      child: _AccountMenu(
                        email: snapshot.data?.email,
                        onChangePassword: _openChangePassword,
                        onLogout: widget.onLogout,
                        extended: wide,
                      ),
                    ),
                  ),
                ),
                destinations: [
                  for (final d in destinations)
                    NavigationRailDestination(
                      icon: Icon(d.icon),
                      selectedIcon: Icon(d.selectedIcon),
                      label: Text(wide ? d.label : d.shortLabel),
                    ),
                ],
              ),
              const VerticalDivider(width: 1),
              Expanded(child: pages),
            ],
          ),
        );
      },
    );
  }

  /// Phones: a bottom [NavigationBar] with the first
  /// [_primaryDestinationCount] modules plus a "More" slot that opens a
  /// sheet listing the rest (Networks, Volumes, Users) and the account
  /// actions the rail shows on wider screens.
  Widget _buildPhoneLayout({
    required List<_ModuleDestination> destinations,
    required int selectedIndex,
    required String? email,
    required Widget body,
  }) {
    final primary = destinations.take(_primaryDestinationCount).toList();
    final overflow = destinations.skip(_primaryDestinationCount).toList();
    final inOverflow = selectedIndex >= primary.length;
    final moreDestination = inOverflow ? destinations[selectedIndex] : null;

    return Scaffold(
      body: body,
      bottomNavigationBar: NavigationBar(
        selectedIndex: inOverflow ? primary.length : selectedIndex,
        labelBehavior: NavigationDestinationLabelBehavior.alwaysShow,
        onDestinationSelected: (i) {
          if (i < primary.length) {
            setState(() => _selectedIndex = i);
          } else {
            _openMoreSheet(
              overflow: overflow,
              firstOverflowIndex: primary.length,
              selectedIndex: selectedIndex,
              email: email,
            );
          }
        },
        destinations: [
          for (final d in primary)
            NavigationDestination(
              icon: Icon(d.icon),
              selectedIcon: Icon(d.selectedIcon),
              label: d.shortLabel,
            ),
          NavigationDestination(
            icon: Icon(moreDestination?.icon ?? Icons.menu),
            selectedIcon: Icon(moreDestination?.selectedIcon ?? Icons.menu),
            label: moreDestination?.shortLabel ?? 'More',
            tooltip: 'More',
          ),
        ],
      ),
    );
  }

  Future<void> _openMoreSheet({
    required List<_ModuleDestination> overflow,
    required int firstOverflowIndex,
    required int selectedIndex,
    required String? email,
  }) async {
    final action = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          children: [
            for (var i = 0; i < overflow.length; i++)
              ListTile(
                leading: Icon(
                  firstOverflowIndex + i == selectedIndex
                      ? overflow[i].selectedIcon
                      : overflow[i].icon,
                ),
                title: Text(overflow[i].label),
                selected: firstOverflowIndex + i == selectedIndex,
                onTap: () => Navigator.of(
                  context,
                ).pop('module:${firstOverflowIndex + i}'),
              ),
            const Divider(),
            if (email != null)
              ListTile(
                leading: const CircleAvatar(
                  radius: 14,
                  child: Icon(Icons.person, size: 16),
                ),
                title: Text(email, overflow: TextOverflow.ellipsis),
                subtitle: const Text('Signed in'),
              ),
            ListTile(
              leading: const Icon(Icons.password),
              title: const Text('Change password'),
              onTap: () => Navigator.of(context).pop('password'),
            ),
            ListTile(
              leading: const Icon(Icons.logout),
              title: const Text('Log out'),
              onTap: () => Navigator.of(context).pop('logout'),
            ),
          ],
        ),
      ),
    );
    if (!mounted || action == null) return;
    if (action.startsWith('module:')) {
      setState(() => _selectedIndex = int.parse(action.substring(7)));
    } else if (action == 'password') {
      _openChangePassword();
    } else if (action == 'logout') {
      widget.onLogout();
    }
  }
}

/// How many modules get their own slot in the phone bottom bar; the rest
/// go behind "More". Four plus "More" keeps each slot wide enough for its
/// label on a 360 px screen.
const _primaryDestinationCount = 4;

class _ModuleDestination {
  final IconData icon;
  final IconData selectedIcon;
  final String label;
  final String shortLabel;
  final WidgetBuilder builder;

  const _ModuleDestination({
    required this.icon,
    required this.selectedIcon,
    required this.label,
    String? shortLabel,
    required this.builder,
  }) : shortLabel = shortLabel ?? label;
}

class _AccountMenu extends StatelessWidget {
  final String? email;
  final VoidCallback onChangePassword;
  final VoidCallback onLogout;
  final bool extended;

  const _AccountMenu({
    required this.email,
    required this.onChangePassword,
    required this.onLogout,
    required this.extended,
  });

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<String>(
      tooltip: 'Account',
      onSelected: (value) {
        if (value == 'password') onChangePassword();
        if (value == 'logout') onLogout();
      },
      itemBuilder: (context) => [
        if (email != null)
          PopupMenuItem<String>(
            enabled: false,
            child: Text(email!, style: Theme.of(context).textTheme.bodySmall),
          ),
        const PopupMenuItem(value: 'password', child: Text('Change password')),
        const PopupMenuItem(value: 'logout', child: Text('Log out')),
      ],
      child: extended
          ? Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Row(
                children: [
                  const CircleAvatar(
                    radius: 14,
                    child: Icon(Icons.person, size: 16),
                  ),
                  const SizedBox(width: 8),
                  ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 140),
                    child: Text(
                      email ?? '',
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.bodySmall,
                    ),
                  ),
                ],
              ),
            )
          : const CircleAvatar(radius: 14, child: Icon(Icons.person, size: 16)),
    );
  }
}
