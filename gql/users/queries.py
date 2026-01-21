import graphene
from .types import UserNode
from users.models import User

class Query(graphene.ObjectType):
    user = graphene.Field(UserNode)
    users = graphene.List(UserNode)

    def resolve_user(self, info):
        user = info.context.user
        if user.is_authenticated:
            return user
        return None

    def resolve_users(self, info):
        return User.objects.all()
