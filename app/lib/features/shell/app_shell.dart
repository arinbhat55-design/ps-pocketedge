import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/user.dart';
import '../containers/container_list_screen.dart';
import '../images/image_list_screen.dart';
import '../servers/server_list_screen.dart';
import '../users/change_password_dialog.dart';
import '../users/user_list_screen.dart';

/// Post-login shell: a persistent left [NavigationRail] listing the app's
/// main modules (Servers, Containers, and — admin only — Users), with the
/// selected module's screen shown alongside it in an [IndexedStack] so
/// switching tabs doesn't lose each module's scroll/search state. Replaces
/// the old design where ServerListScreen was the sole home screen and
/// every other module was reached by pushing/popping from its AppBar
/// icons.
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
            builder: (_) => ServerListScreen(apiClient: widget.apiClient),
          ),
          _ModuleDestination(
            icon: Icons.view_in_ar_outlined,
            selectedIcon: Icons.view_in_ar,
            label: 'Containers',
            builder: (_) => ContainerListScreen(apiClient: widget.apiClient),
          ),
          _ModuleDestination(
            icon: Icons.inventory_2_outlined,
            selectedIcon: Icons.inventory_2,
            label: 'Images',
            builder: (_) => ImageListScreen(
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
        final wide = MediaQuery.of(context).size.width > 800;

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
                      label: Text(d.label),
                    ),
                ],
              ),
              const VerticalDivider(width: 1),
              Expanded(
                child: IndexedStack(
                  index: selectedIndex,
                  children: [for (final d in destinations) d.builder(context)],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _ModuleDestination {
  final IconData icon;
  final IconData selectedIcon;
  final String label;
  final WidgetBuilder builder;

  const _ModuleDestination({
    required this.icon,
    required this.selectedIcon,
    required this.label,
    required this.builder,
  });
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
                  Expanded(
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
