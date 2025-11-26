-- Insert default tenant and workspace for basic system operation

INSERT OR IGNORE INTO tenants (id, name)
VALUES (1, 'Default Tenant');

INSERT OR IGNORE INTO workspaces (id, name)
VALUES (1, 'Default Workspace');
