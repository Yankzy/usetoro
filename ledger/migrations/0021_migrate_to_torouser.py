from django.conf import settings
from django.db import migrations, models
import django.db.models.deletion


MIGRATE_FK_COLUMNS_SQL = """
DO $$
DECLARE
    r RECORD;
BEGIN
    -- 1. Drop existing FK constraints pointing to auth_user
    FOR r IN (
        SELECT tc.table_schema, tc.table_name, tc.constraint_name
        FROM information_schema.table_constraints tc
        JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = tc.constraint_name
        WHERE tc.constraint_type = 'FOREIGN KEY'
          AND tc.table_name IN (
              'ledger_entitymodel',
              'ledger_entitymanagementmodel',
              'ledger_bankaccountmodel',
              'ledger_plaiditem',
              'ledger_transactionauditlog',
              'django_admin_log'
          )
          AND ccu.table_name = 'auth_user'
    ) LOOP
        EXECUTE format('ALTER TABLE %I.%I DROP CONSTRAINT IF EXISTS %I;', r.table_schema, r.table_name, r.constraint_name);
    END LOOP;

    -- 2. Migrate column types from integer to UUID, mapping existing users by email if auth_user exists
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'auth_user') AND
       EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'toro_core' AND table_name = 'users') THEN

        -- ledger_entitymodel.admin_id
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_entitymodel' AND column_name = 'admin_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_entitymodel ADD COLUMN IF NOT EXISTS admin_uuid UUID;
            UPDATE ledger_entitymodel e
            SET admin_uuid = u.id
            FROM auth_user a
            JOIN toro_core.users u ON LOWER(u.email) = LOWER(a.email)
            WHERE e.admin_id::text = a.id::text;

            ALTER TABLE ledger_entitymodel DROP COLUMN admin_id;
            ALTER TABLE ledger_entitymodel RENAME COLUMN admin_uuid TO admin_id;
        END IF;

        -- ledger_entitymanagementmodel.user_id
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_entitymanagementmodel' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_entitymanagementmodel ADD COLUMN IF NOT EXISTS user_uuid UUID;
            UPDATE ledger_entitymanagementmodel m
            SET user_uuid = u.id
            FROM auth_user a
            JOIN toro_core.users u ON LOWER(u.email) = LOWER(a.email)
            WHERE m.user_id::text = a.id::text;

            ALTER TABLE ledger_entitymanagementmodel DROP COLUMN user_id;
            ALTER TABLE ledger_entitymanagementmodel RENAME COLUMN user_uuid TO user_id;
        END IF;

        -- ledger_bankaccountmodel.user_id
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_bankaccountmodel' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_bankaccountmodel ADD COLUMN IF NOT EXISTS user_uuid UUID;
            UPDATE ledger_bankaccountmodel b
            SET user_uuid = u.id
            FROM auth_user a
            JOIN toro_core.users u ON LOWER(u.email) = LOWER(a.email)
            WHERE b.user_id::text = a.id::text;

            ALTER TABLE ledger_bankaccountmodel DROP COLUMN user_id;
            ALTER TABLE ledger_bankaccountmodel RENAME COLUMN user_uuid TO user_id;
        END IF;

        -- ledger_plaiditem.user_id
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_plaiditem' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_plaiditem ADD COLUMN IF NOT EXISTS user_uuid UUID;
            UPDATE ledger_plaiditem p
            SET user_uuid = u.id
            FROM auth_user a
            JOIN toro_core.users u ON LOWER(u.email) = LOWER(a.email)
            WHERE p.user_id::text = a.id::text;

            ALTER TABLE ledger_plaiditem DROP COLUMN user_id;
            ALTER TABLE ledger_plaiditem RENAME COLUMN user_uuid TO user_id;
        END IF;

        -- ledger_transactionauditlog.user_id
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_transactionauditlog' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_transactionauditlog ADD COLUMN IF NOT EXISTS user_uuid UUID;
            UPDATE ledger_transactionauditlog t
            SET user_uuid = u.id
            FROM auth_user a
            JOIN toro_core.users u ON LOWER(u.email) = LOWER(a.email)
            WHERE t.user_id::text = a.id::text;

            ALTER TABLE ledger_transactionauditlog DROP COLUMN user_id;
            ALTER TABLE ledger_transactionauditlog RENAME COLUMN user_uuid TO user_id;
        END IF;
        -- django_admin_log.user_id
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'django_admin_log' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE django_admin_log ADD COLUMN IF NOT EXISTS user_uuid UUID;
            UPDATE django_admin_log d
            SET user_uuid = u.id
            FROM auth_user a
            JOIN toro_core.users u ON LOWER(u.email) = LOWER(a.email)
            WHERE d.user_id::text = a.id::text;

            ALTER TABLE django_admin_log DROP COLUMN user_id;
            ALTER TABLE django_admin_log RENAME COLUMN user_uuid TO user_id;
        END IF;
    ELSE
        -- Fallback: convert columns directly to UUID
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_entitymodel' AND column_name = 'admin_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_entitymodel ALTER COLUMN admin_id TYPE uuid USING (NULL::uuid);
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_entitymanagementmodel' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_entitymanagementmodel ALTER COLUMN user_id TYPE uuid USING (NULL::uuid);
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_bankaccountmodel' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_bankaccountmodel ALTER COLUMN user_id TYPE uuid USING (NULL::uuid);
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_plaiditem' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_plaiditem ALTER COLUMN user_id TYPE uuid USING (NULL::uuid);
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ledger_transactionauditlog' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE ledger_transactionauditlog ALTER COLUMN user_id TYPE uuid USING (NULL::uuid);
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'django_admin_log' AND column_name = 'user_id' AND data_type != 'uuid') THEN
            ALTER TABLE django_admin_log ALTER COLUMN user_id TYPE uuid USING (NULL::uuid);
        END IF;
    END IF;

    -- 3. Add FK constraints to toro_core.users(id) if toro_core.users table exists
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'toro_core' AND table_name = 'users') THEN
        IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'ledger_entitymodel') THEN
            ALTER TABLE ledger_entitymodel DROP CONSTRAINT IF EXISTS fk_ledger_entitymodel_admin_torouser;
            ALTER TABLE ledger_entitymodel ADD CONSTRAINT fk_ledger_entitymodel_admin_torouser
                FOREIGN KEY (admin_id) REFERENCES toro_core.users(id) ON DELETE CASCADE;
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'ledger_entitymanagementmodel') THEN
            ALTER TABLE ledger_entitymanagementmodel DROP CONSTRAINT IF EXISTS fk_ledger_entitymanagementmodel_user_torouser;
            ALTER TABLE ledger_entitymanagementmodel ADD CONSTRAINT fk_ledger_entitymanagementmodel_user_torouser
                FOREIGN KEY (user_id) REFERENCES toro_core.users(id) ON DELETE CASCADE;
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'ledger_bankaccountmodel') THEN
            ALTER TABLE ledger_bankaccountmodel DROP CONSTRAINT IF EXISTS fk_ledger_bankaccountmodel_user_torouser;
            ALTER TABLE ledger_bankaccountmodel ADD CONSTRAINT fk_ledger_bankaccountmodel_user_torouser
                FOREIGN KEY (user_id) REFERENCES toro_core.users(id) ON DELETE CASCADE;
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'ledger_plaiditem') THEN
            ALTER TABLE ledger_plaiditem DROP CONSTRAINT IF EXISTS fk_ledger_plaiditem_user_torouser;
            ALTER TABLE ledger_plaiditem ADD CONSTRAINT fk_ledger_plaiditem_user_torouser
                FOREIGN KEY (user_id) REFERENCES toro_core.users(id) ON DELETE CASCADE;
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'ledger_transactionauditlog') THEN
            ALTER TABLE ledger_transactionauditlog DROP CONSTRAINT IF EXISTS fk_ledger_transactionauditlog_user_torouser;
            ALTER TABLE ledger_transactionauditlog ADD CONSTRAINT fk_ledger_transactionauditlog_user_torouser
                FOREIGN KEY (user_id) REFERENCES toro_core.users(id) ON DELETE CASCADE;
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'django_admin_log') THEN
            ALTER TABLE django_admin_log DROP CONSTRAINT IF EXISTS fk_django_admin_log_user_torouser;
            ALTER TABLE django_admin_log ADD CONSTRAINT fk_django_admin_log_user_torouser
                FOREIGN KEY (user_id) REFERENCES toro_core.users(id) ON DELETE CASCADE;
        END IF;
    END IF;
END $$;
"""


class Migration(migrations.Migration):

    dependencies = [
        ("toro_core", "0001_initial"),
        ("ledger", "0020_bookkeepingnotificationdelivery"),
    ]

    operations = [
        migrations.RunSQL(
            sql=MIGRATE_FK_COLUMNS_SQL,
            reverse_sql=migrations.RunSQL.noop,
        ),
        migrations.AlterField(
            model_name="entitymodel",
            name="admin",
            field=models.ForeignKey(
                on_delete=django.db.models.deletion.CASCADE,
                related_name="admin_of",
                to=settings.AUTH_USER_MODEL,
                verbose_name="Admin",
            ),
        ),
        migrations.AlterField(
            model_name="entitymanagementmodel",
            name="user",
            field=models.ForeignKey(
                on_delete=django.db.models.deletion.CASCADE,
                related_name="entity_permissions",
                to=settings.AUTH_USER_MODEL,
                verbose_name="Manager",
            ),
        ),
        migrations.AlterField(
            model_name="bankaccountmodel",
            name="user",
            field=models.ForeignKey(
                null=True,
                on_delete=django.db.models.deletion.CASCADE,
                to=settings.AUTH_USER_MODEL,
            ),
        ),
        migrations.AlterField(
            model_name="plaiditem",
            name="user",
            field=models.ForeignKey(
                on_delete=django.db.models.deletion.CASCADE,
                to=settings.AUTH_USER_MODEL,
            ),
        ),
        migrations.AlterField(
            model_name="transactionauditlog",
            name="user",
            field=models.ForeignKey(
                on_delete=django.db.models.deletion.CASCADE,
                to=settings.AUTH_USER_MODEL,
            ),
        ),
    ]
