from typing import Any, ClassVar
from uuid import uuid4
from django.contrib.auth.base_user import AbstractBaseUser, BaseUserManager
from django.db import models


class ToroUserManager(BaseUserManager["ToroUser"]):
    def create_user(
        self,
        email: str,
        password: str | None = None,
        **extra_fields: Any,
    ) -> "ToroUser":
        if not email:
            raise ValueError("The Email field must be set")
        email = self.normalize_email(email).lower()

        # Discard legacy Django username if passed (e.g. from existing test fixtures)
        extra_fields.pop("username", None)

        entity_id = extra_fields.get("entity_id")
        if not entity_id:
            from django.db import connection

            entity_id = uuid4()
            with connection.cursor() as cur:
                cur.execute(
                    """
                    INSERT INTO toro_core.entities (id, name, entity_type)
                    VALUES (%s, 'Default Entity', 'client')
                    ON CONFLICT (id) DO NOTHING;
                    """,
                    [entity_id],
                )
            extra_fields["entity_id"] = entity_id

        user = self.model(email=email, **extra_fields)
        if password:
            user.set_password(password)
        else:
            user.set_unusable_password()
        user.save(using=self._db)
        return user

    def create_superuser(
        self,
        email: str,
        password: str | None = None,
        **extra_fields: Any,
    ) -> "ToroUser":
        extra_fields.setdefault("role", "owner")
        return self.create_user(email, password, **extra_fields)


class ToroUser(AbstractBaseUser):
    """
    Unmanaged Django User model mapping directly to canonical postgres table toro_core.users.
    Schema and DDL are authoritatively governed by Goose migrations (sql/schema/001_toro_core.sql).
    """

    id = models.UUIDField(  # pyrefly: ignore[bad-override-mutable-attribute]  # type: ignore[override]
        primary_key=True, default=uuid4, editable=False
    )
    entity_id = models.UUIDField()
    email = models.EmailField(unique=True)
    password = models.CharField(max_length=255, db_column="password_hash")
    full_name = models.CharField(max_length=255, null=True, blank=True)
    role = models.CharField(max_length=50, default="member")
    user_type = models.CharField(max_length=50, default="standard")
    is_active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    # toro_core.users has no last_login column; disable it on AbstractBaseUser
    last_login = None  # pyrefly: ignore[bad-assignment]  # type: ignore[assignment]

    USERNAME_FIELD: ClassVar[str] = "email"
    EMAIL_FIELD: ClassVar[str] = "email"
    REQUIRED_FIELDS: ClassVar[list[str]] = []

    objects = ToroUserManager()  # pyrefly: ignore[bad-assignment]  # type: ignore[assignment]

    class Meta:
        db_table = '"toro_core"."users"'
        managed = False
        verbose_name = "Toro User"
        verbose_name_plural = "Toro Users"

    @property
    def is_staff(self) -> bool:
        return self.role in ("owner", "admin")

    @property
    def is_superuser(self) -> bool:
        return self.role == "owner"

    def has_perm(self, perm: str, obj: Any = None) -> bool:
        return self.is_active and (self.is_staff or self.is_superuser)

    def has_module_perms(self, app_label: str) -> bool:
        return self.is_active and (self.is_staff or self.is_superuser)

    def get_full_name(self) -> str:
        return self.full_name or self.email

    def get_short_name(self) -> str:
        return self.full_name or self.email

    def __str__(self) -> str:
        return f"ToroUser({self.email}, id={self.id}, role={self.role})"
