import uuid
from django.db import migrations, models


class Migration(migrations.Migration):

    initial = True

    dependencies = []

    operations = [
        migrations.RunSQL(
            sql="""
            CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
            CREATE SCHEMA IF NOT EXISTS toro_core;
            CREATE TABLE IF NOT EXISTS toro_core.entities (
                id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
                parent_id UUID,
                name VARCHAR(255) NOT NULL,
                entity_type VARCHAR(50) NOT NULL DEFAULT 'client',
                plan_tier TEXT DEFAULT 'basic',
                status TEXT DEFAULT 'active',
                created_at TIMESTAMPTZ DEFAULT NOW(),
                updated_at TIMESTAMPTZ DEFAULT NOW()
            );
            CREATE TABLE IF NOT EXISTS toro_core.users (
                id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
                entity_id UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
                email TEXT UNIQUE NOT NULL,
                password_hash TEXT NOT NULL DEFAULT '',
                full_name TEXT,
                role TEXT DEFAULT 'member',
                user_type TEXT NOT NULL DEFAULT 'standard',
                is_active BOOLEAN DEFAULT TRUE,
                created_at TIMESTAMPTZ DEFAULT NOW(),
                updated_at TIMESTAMPTZ DEFAULT NOW()
            );
            """,
            reverse_sql=migrations.RunSQL.noop,
        ),
        migrations.CreateModel(
            name="ToroUser",
            fields=[
                ("id", models.UUIDField(default=uuid.uuid4, editable=False, primary_key=True, serialize=False)),
                ("entity_id", models.UUIDField()),
                ("email", models.EmailField(max_length=254, unique=True)),
                ("password", models.CharField(db_column="password_hash", max_length=255)),
                ("full_name", models.CharField(blank=True, max_length=255, null=True)),
                ("role", models.CharField(default="member", max_length=50)),
                ("user_type", models.CharField(default="standard", max_length=50)),
                ("is_active", models.BooleanField(default=True)),
                ("created_at", models.DateTimeField(auto_now_add=True)),
                ("updated_at", models.DateTimeField(auto_now=True)),
            ],
            options={
                "verbose_name": "Toro User",
                "verbose_name_plural": "Toro Users",
                "db_table": '"toro_core"."users"',
                "managed": False,
                "swappable": "AUTH_USER_MODEL",
            },
        ),
    ]
