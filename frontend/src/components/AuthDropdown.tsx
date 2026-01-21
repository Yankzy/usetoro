import React, { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { useLoginUserMutation } from '../store/user/userEndpoints';
import { Icons } from './Icons';

export const AuthDropdown = () => {
    const [isOpen, setIsOpen] = useState(false);
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [login, { isLoading, error }] = useLoginUserMutation();
    const navigate = useNavigate();

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        try {
            await login({ email: username, password }).unwrap();
            navigate('/dashboard');
        } catch (err) {
            console.error('Login failed', err);
        }
    };

    return (
        <div className="relative">
            <button
                onClick={() => setIsOpen(!isOpen)}
                className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-zinc-300 bg-zinc-900 rounded-lg border border-zinc-800 hover:bg-zinc-800 hover:text-white transition-colors"
            >
                <Icons.User className="w-4 h-4" />
                <span>Sign in</span>
            </button>

            {isOpen && (
                <>
                    <div className="fixed inset-0 z-40" onClick={() => setIsOpen(false)} />
                    <div className="absolute right-0 mt-2 w-80 bg-white dark:bg-zinc-900 rounded-lg shadow-xl border border-zinc-200 dark:border-toro-border z-50 p-6 animate-in fade-in zoom-in-95 duration-200 origin-top-right">
                        <form onSubmit={handleSubmit} className="space-y-4">
                            <div>
                                <input
                                    type="text"
                                    required
                                    className="w-full px-3 py-2 text-sm bg-zinc-50 dark:bg-zinc-950 border border-zinc-300 dark:border-zinc-800 rounded-md focus:outline-none focus:ring-2 focus:ring-toro-green text-zinc-900 dark:text-white"
                                    placeholder="Username"
                                    value={username}
                                    onChange={(e) => setUsername(e.target.value)}
                                />
                            </div>
                            <div>
                                <input
                                    type="password"
                                    required
                                    className="w-full px-3 py-2 text-sm bg-zinc-50 dark:bg-zinc-950 border border-zinc-300 dark:border-zinc-800 rounded-md focus:outline-none focus:ring-2 focus:ring-toro-green text-zinc-900 dark:text-white"
                                    placeholder="Password"
                                    value={password}
                                    onChange={(e) => setPassword(e.target.value)}
                                />
                            </div>

                            {error && (
                                <div className="text-red-500 text-xs text-center">
                                    Login failed.
                                </div>
                            )}

                            <button
                                type="submit"
                                disabled={isLoading}
                                className={`w-full py-2 px-4 bg-toro-green hover:bg-green-600 text-white text-sm font-medium rounded-md transition-colors ${isLoading ? 'opacity-70 cursor-wait' : ''}`}
                            >
                                {isLoading ? 'Signing in...' : 'Sign in'}
                            </button>

                            <div className="text-center text-xs text-zinc-500 hover:text-zinc-400">
                                <Link to="/forgot-password">Forgot your password?</Link>
                            </div>
                        </form>
                    </div>
                </>
            )}
        </div>
    );
};
