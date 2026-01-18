import os
import django

os.environ.setdefault("DJANGO_SETTINGS_MODULE", "config.settings")
django.setup()

from django.contrib.auth import get_user_model
from users.models import Activity

User = get_user_model()

# Create Superuser
email = 'admin@example.com'
password = 'admin_password'
if not User.objects.filter(email=email).exists():
    u = User.objects.create_superuser(email, password)
    print(f"Superuser created: {u}")
else:
    u = User.objects.get(email=email)
    print(f"Superuser exists: {u}")

# Verify Activity
# Let's create an activity where user likes themselves (narcissistic, but valid context)
activity, created = Activity.like(u, u)
print(f"Activity created: {activity}, Created now: {created}")

count = Activity.objects.count()
print(f"Total Activities: {count}")

assert count >= 1
print("Verification ALL GOOD")
