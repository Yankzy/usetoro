import React, { useState } from 'react';
import { useLoginMutation } from '../store/auth/authApi';
import { useNavigate } from 'react-router-dom';

const Login = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [login, { isLoading, error }] = useLoginMutation();
    const navigate = useNavigate();

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        try {
            await login({ username, password }).unwrap();
            navigate('/dashboard');
        } catch (err) {
            console.error('Login failed', err);
        }
    };

    return (
        <div className="flex min-h-screen items-center justify-center bg-toro-bg">
            <div className="w-full max-w-md space-y-8 rounded-xl border border-toro-border bg-toro-card p-8 shadow-2xl">
                <div className="text-center">
                    <h2 className="mt-2 text-3xl font-bold text-white">Sign in to your account</h2>
                </div>
                <form className="mt-8 space-y-6" onSubmit={handleSubmit}>
                    <div className="space-y-4 rounded-md shadow-sm">
                        <div>
                            <input
                                type="text"
                                required
                                className="relative block w-full appearance-none rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-3 text-white placeholder-zinc-500 focus:z-10 focus:border-toro-green focus:outline-none focus:ring-toro-green sm:text-sm"
                                placeholder="Username"
                                value={username}
                                onChange={(e) => setUsername(e.target.value)}
                            />
                        </div>
                        <div>
                            <input
                                type="password"
                                required
                                className="relative block w-full appearance-none rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-3 text-white placeholder-zinc-500 focus:z-10 focus:border-toro-green focus:outline-none focus:ring-toro-green sm:text-sm"
                                placeholder="Password"
                                value={password}
                                onChange={(e) => setPassword(e.target.value)}
                            />
                        </div>
                    </div>

                    {error && (
                        <div className="text-red-500 text-sm text-center">
                            Login failed. Please check your credentials.
                        </div>
                    )}

                    <div>
                        <button
                            type="submit"
                            disabled={isLoading}
                            className={`group relative flex w-full justify-center rounded-lg bg-toro-green px-4 py-3 text-sm font-semibold text-white hover:bg-green-500 focus:outline-none focus:ring-2 focus:ring-toro-green focus:ring-offset-2 focus:ring-offset-gray-900 ${isLoading ? 'opacity-70 cursor-wait' : ''}`}
                        >
                            {isLoading ? 'Signing in...' : 'Sign in'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    );
};

export default Login;
