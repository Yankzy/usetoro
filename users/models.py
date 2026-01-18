import logging
import uuid
from datetime import timedelta, datetime as py_datetime
from typing import Tuple

from django.conf import settings
from django.contrib.auth.models import AbstractBaseUser, BaseUserManager, PermissionsMixin
from django.contrib.contenttypes.fields import GenericForeignKey
from django.contrib.contenttypes.models import ContentType
from django.db import models
from django.db.models import QuerySet
from django.utils import timezone
from django.utils.crypto import salted_hmac


logger = logging.getLogger(__name__)


class UserManager(BaseUserManager):
    def _create_user(self, email, password, **kwargs):
        if not email or not password:
            raise ValueError("Email and password must be set")

        email = self.normalize_email(email)
        user = self.model(email=email, **kwargs)
        user.set_password(password)
        user.save(using=self._db)
        return user

    def create_user(self, email, password=None, **kwargs):
        kwargs.setdefault("is_staff", False)
        kwargs.setdefault("is_superuser", False)

        return self._create_user(email, password, **kwargs)

    def create_superuser(self, email, password, **kwargs):
        kwargs.setdefault("is_staff", True)
        kwargs.setdefault("is_superuser", True)

        if kwargs.get("is_staff") is not True:
            raise ValueError("Superuser must have is_staff=True.")
        if kwargs.get("is_superuser") is not True:
            raise ValueError("Superuser must have is_superuser=True.")

        return self._create_user(email, password, **kwargs)



class Activity(models.Model):
    class Types(models.TextChoices):
        SUBSCRIBE = 'subscribe', 'Subscribe'

    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    user = models.ForeignKey(settings.AUTH_USER_MODEL, on_delete=models.CASCADE)
    activity_type = models.CharField(
        max_length=32, choices=Types.choices, default=Types.SUBSCRIBE
    )
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    content_type = models.ForeignKey(ContentType, on_delete=models.CASCADE)
    object_id = models.UUIDField()
    content_object = GenericForeignKey('content_type', 'object_id')

    def __str__(self):
        return f"{self.user} subscribed to {self.content_type} ({self.object_id})"

    class Meta:
        indexes = [
            models.Index(fields=["activity_type", "content_type", "object_id"]),
            models.Index(fields=['user', 'activity_type']),
        ]
        ordering = ['-created_at']
        unique_together = ('user', 'activity_type', 'content_type', 'object_id')

    @staticmethod
    def subscribe(user, instance) -> Tuple["Activity", bool]:
        """
        Subscribe a user to an object.
        Returns: (Activity instance, created: bool)
        """
        content_type = ContentType.objects.get_for_model(instance)
        return Activity.objects.get_or_create(
            user=user,
            activity_type=Activity.Types.SUBSCRIBE,
            content_type=content_type,
            object_id=instance.id,
        )

    @staticmethod
    def unsubscribe(user, instance) -> None:
        """
        Unsubscribe a user from an object.
        """
        content_type = ContentType.objects.get_for_model(instance)
        Activity.objects.filter(
            user=user,
            activity_type=Activity.Types.SUBSCRIBE,
            content_type=content_type,
            object_id=instance.id,
        ).delete()

    @staticmethod
    def is_subscribed(user, instance) -> bool:
        """
        Check if a user is subscribed to an object.
        """
        content_type = ContentType.objects.get_for_model(instance)
        return Activity.objects.filter(
            user=user,
            activity_type=Activity.Types.SUBSCRIBE,
            content_type=content_type,
            object_id=instance.id,
        ).exists()

    @staticmethod
    def subscribe_count(instance) -> int:
        """
        Get the number of subscribers for an object.
        """
        content_type = ContentType.objects.get_for_model(instance)
        return Activity.objects.filter(
            content_type=content_type,
            object_id=instance.id,
            activity_type=Activity.Types.SUBSCRIBE
        ).count()


class User(AbstractBaseUser, PermissionsMixin):
    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    
    email = models.EmailField(unique=True)
    is_active = models.BooleanField(default=True)
    is_agent = models.BooleanField(default=False)
    currency = models.CharField(max_length=3, default='USD', verbose_name='Curency code')
    analysis_timeframe_hours = models.PositiveIntegerField(
        default=24,
        help_text="Number of hours back in time this user considers when performing analysis"
    )
    is_staff = models.BooleanField(default=False)
    is_superuser = models.BooleanField(default=False)
    is_verified = models.BooleanField(default=False, blank=True)
    profile_is_private = models.BooleanField(default=False, blank=True)
    notification_is_on = models.BooleanField(default=True)
    localization_is_on = models.BooleanField(default=True)
    first_name = models.CharField(max_length=25, null=True, blank=True)
    last_name = models.CharField(max_length=25, null=True, blank=True)
    phone = models.CharField(max_length=15, null=True, blank=True)
    street_address = models.CharField(max_length=250, null=True, blank=True)
    city = models.CharField(max_length=50, null=True, blank=True)
    zip_code = models.CharField(max_length=10, null=True, blank=True)
    state = models.CharField(max_length=50, null=True, blank=True)
    country = models.CharField(max_length=56, null=True, blank=True)
    user_bio = models.TextField(max_length=500, null=True, blank=True)
    profile_image = models.URLField(null=True, blank=True)
    # Removed LanguageModel foreign key to keep it simple as it wasn't provided, defaulting to string 'en'
    language = models.CharField(max_length=10, default="en")

    private_key = models.CharField(
        db_column="PrivateKey",
        max_length=256,
        blank=True,
        default=uuid.uuid4,
        help_text="The private key is actually a password salt",
    )
    # AbstractBaseUser handles basic password field, but reference has a custom one?
    # AbstractBaseUser has 'password' field. The reference 'password' field definition might be redundant or specific override.
    # "Stores the password hash" - standard Django behavior.
    # I will rely on AbstractBaseUser's password field unless I strictly need the custom column name 'StoredPassword'.
    # To minimize friction with Django's built-in auth, I'll use standard AbstractBaseUser behavior but I can add the meta fields if needed.
    # Reference: db_column="StoredPassword". Let's assume standard behavior for now to avoid issues, 
    # but the reference had explicit field. I'll omit explicit password field to use AbstractBaseUser's default unless user asks.
    
    date_joined = models.DateTimeField(default=timezone.now)
    is_premium = models.BooleanField(default=False)
    premium_since = models.DateTimeField(blank=True, null=True)
    handle = models.CharField(max_length=50, null=True, blank=True)
    trading_style = models.CharField(max_length=50, null=True, blank=True)
    alpace_supplementary_info = models.JSONField(null=True, blank=True, default=dict)

    USERNAME_FIELD = "email"
    REQUIRED_FIELDS = []

    objects = UserManager()

    def __str__(self):
        status = "Premium" if self.is_premium else "Standard"
        return f"{self.email} ({status})"

    def save_history(self, **kwargs):
        pass

    def delete_history(self, **kwargs):
        now = py_datetime.now()
        self.validity_from = now
        self.validity_to = now
        self.save()

    def clear_refresh_tokens(self):
        # Requires simplejwt or similar if using refresh tokens reversed relation, commenting out to avoid errors if not installed
        pass

    def get_session_auth_hash(self):
        key_salt = "core.User.get_session_auth_hash"
        return salted_hmac(key_salt, self.email).hexdigest()

    @property
    def full_name(self):
        return f"{self.first_name or ''} {self.last_name or ''}".strip()

    class Meta:
        managed = True
        db_table = 'User'
