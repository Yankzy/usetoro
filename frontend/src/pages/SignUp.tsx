import React, { useState } from 'react';
import { useRegisterUserMutation } from '../store/user/userEndpoints';
import { useNavigate, Link } from 'react-router-dom';
import { Navbar } from '../components/Navbar';

const SignUp = () => {
    const [email, setEmail] = useState('');
    const [password, setPassword] = useState('');
    const [firstName, setFirstName] = useState('');
    const [lastName, setLastName] = useState('');
    const [register, { isLoading, error }] = useRegisterUserMutation();
    const navigate = useNavigate();

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        try {
            await register({ email, password, first_name: firstName, last_name: lastName }).unwrap();
            // Redirect to login on success
            navigate('/');
        } catch (err) {
            console.error('Registration failed', err);
        }
    };

    return (
        <div className="flex min-h-screen items-center justify-center bg-toro-bg">
            <Navbar />
            <div className="w-full max-w-md space-y-8 rounded-xl border border-toro-border bg-toro-card p-8 shadow-2xl mt-20">
                <div className="text-center">
                    <h2 className="mt-2 text-3xl font-bold text-white">Create your account</h2>
                    <p className="mt-2 text-sm text-gray-400">
                        Or <Link to="/" className="font-medium text-toro-green hover:text-green-400">sign in to your existing account</Link>
                    </p>
                </div>
                <form className="mt-8 space-y-6" onSubmit={handleSubmit}>
                    <div className="space-y-4 rounded-md shadow-sm">
                        <div className="flex gap-4">
                            <div className="flex-1">
                                <input
                                    type="text"
                                    className="relative block w-full appearance-none rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-3 text-white placeholder-zinc-500 focus:z-10 focus:border-toro-green focus:outline-none focus:ring-toro-green sm:text-sm"
                                    placeholder="First Name"
                                    value={firstName}
                                    onChange={(e) => setFirstName(e.target.value)}
                                />
                            </div>
                            <div className="flex-1">
                                <input
                                    type="text"
                                    className="relative block w-full appearance-none rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-3 text-white placeholder-zinc-500 focus:z-10 focus:border-toro-green focus:outline-none focus:ring-toro-green sm:text-sm"
                                    placeholder="Last Name"
                                    value={lastName}
                                    onChange={(e) => setLastName(e.target.value)}
                                />
                            </div>
                        </div>
                        <div>
                            <input
                                type="email"
                                required
                                className="relative block w-full appearance-none rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-3 text-white placeholder-zinc-500 focus:z-10 focus:border-toro-green focus:outline-none focus:ring-toro-green sm:text-sm"
                                placeholder="Email address"
                                value={email}
                                onChange={(e) => setEmail(e.target.value)}
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
                            Registration failed. Please try again.
                        </div>
                    )}

                    <div>
                        <button
                            type="submit"
                            disabled={isLoading}
                            className={`group relative flex w-full justify-center rounded-lg bg-toro-green px-4 py-3 text-sm font-semibold text-white hover:bg-green-500 focus:outline-none focus:ring-2 focus:ring-toro-green focus:ring-offset-2 focus:ring-offset-gray-900 ${isLoading ? 'opacity-70 cursor-wait' : ''}`}
                        >
                            {isLoading ? 'Creating Account...' : 'Sign up'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    );
};

export default SignUp;
