export interface User {
    id: string;
    email: string;
    firstName: string;
    lastName: string;
    dateJoined: string;
    lastLogin: string;
}

export interface GetUsersResponse {
    users: User[];
}
