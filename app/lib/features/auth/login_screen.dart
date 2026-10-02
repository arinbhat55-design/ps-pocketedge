import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../theme/app_theme.dart';
import '../../widgets/app_logo.dart';

class LoginScreen extends StatefulWidget {
  final ApiClient apiClient;
  final void Function(String token) onLoggedIn;

  /// Offered when the control plane runs on this computer, which may grant a
  /// session without login. Null hides the option.
  final void Function(String token)? onLocalSession;

  /// Why the user is seeing this screen, e.g. their session ended.
  final String? notice;

  const LoginScreen({
    super.key,
    required this.apiClient,
    required this.onLoggedIn,
    this.onLocalSession,
    this.notice,
  });

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _formKey = GlobalKey<FormState>();
  final _emailController = TextEditingController();
  final _passwordController = TextEditingController();
  bool _loading = false;
  String? _error;

  @override
  void dispose() {
    _emailController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final token = await widget.apiClient.login(
        _emailController.text.trim(),
        _passwordController.text,
      );
      widget.onLoggedIn(token);
    } catch (e) {
      setState(() => _error = 'Login failed: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _useLocalAccess() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      widget.onLocalSession!(await widget.apiClient.localSession());
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (e) {
      if (mounted) {
        setState(() => _error = 'Could not reach the control plane: $e');
      }
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 360),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Form(
              key: _formKey,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  const Center(child: AppLogo(size: 112)),
                  const SizedBox(height: 16),
                  Text(
                    'PS-pocketEdge',
                    style: Theme.of(context).textTheme.headlineSmall,
                    textAlign: TextAlign.center,
                  ),
                  if (widget.notice != null) ...[
                    const SizedBox(height: 12),
                    Text(widget.notice!, textAlign: TextAlign.center),
                  ],
                  const SizedBox(height: 24),
                  TextFormField(
                    controller: _emailController,
                    decoration: const InputDecoration(labelText: 'Email'),
                    keyboardType: TextInputType.emailAddress,
                    validator: (v) =>
                        (v == null || v.isEmpty) ? 'Required' : null,
                  ),
                  const SizedBox(height: 12),
                  TextFormField(
                    controller: _passwordController,
                    decoration: const InputDecoration(labelText: 'Password'),
                    obscureText: true,
                    validator: (v) =>
                        (v == null || v.isEmpty) ? 'Required' : null,
                    onFieldSubmitted: (_) => _submit(),
                  ),
                  if (_error != null) ...[
                    const SizedBox(height: 12),
                    Text(
                      _error!,
                      style: const TextStyle(color: AppColors.failed),
                    ),
                  ],
                  const SizedBox(height: 24),
                  FilledButton(
                    onPressed: _loading ? null : _submit,
                    child: _loading
                        ? const SizedBox(
                            width: 20,
                            height: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Text('Log in'),
                  ),
                  if (widget.onLocalSession != null) ...[
                    const SizedBox(height: 8),
                    TextButton(
                      onPressed: _loading ? null : _useLocalAccess,
                      child: const Text('Use this computer without signing in'),
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
