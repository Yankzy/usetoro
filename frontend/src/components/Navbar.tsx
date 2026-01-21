import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Icons } from './Icons';
import { AuthDropdown } from './AuthDropdown';

export const Navbar = () => {
    const [isMenuOpen, setIsMenuOpen] = useState(false);

    const toggleMenu = () => setIsMenuOpen(!isMenuOpen);

    return (
        <nav className="fixed top-0 w-full z-50 bg-[#050505]/80 backdrop-blur-md border-b border-toro-border">
            <div className="flex items-center justify-between px-6 py-4 max-w-7xl mx-auto">
                <Link to="/" className="flex items-center gap-2 font-mono text-xl font-bold tracking-tighter cursor-pointer hover:opacity-80 transition-opacity">
                    <Icons.Logo className="w-8 h-8 text-green-500" />
                    <span className="pt-1 text-white">TORO</span>
                </Link>

                <div className="hidden md:flex gap-8 text-sm font-medium text-zinc-400">
                    <a href="/#problem" className="hover:text-white transition-colors">The Trap</a>
                    <a href="/#features" className="hover:text-white transition-colors">Features</a>
                    <a href="/#pricing" className="hover:text-white transition-colors">Pricing</a>
                    <a href="/#faq" className="hover:text-white transition-colors">FAQ</a>
                </div>

                <div className="hidden md:flex gap-6 items-center">
                    <Link to="/signup" className="text-sm font-bold text-zinc-400 hover:text-white transition-colors">
                        Sign up
                    </Link>
                    <AuthDropdown />
                </div>

                <button className="md:hidden flex flex-col justify-center gap-1.5 w-8 h-8 z-50" onClick={toggleMenu}>
                    <span className={`block w-full h-0.5 bg-white transition-all duration-300 ${isMenuOpen ? 'rotate-45 translate-y-2' : ''}`}></span>
                    <span className={`block w-full h-0.5 bg-white transition-all duration-300 ${isMenuOpen ? 'opacity-0' : ''}`}></span>
                    <span className={`block w-full h-0.5 bg-white transition-all duration-300 ${isMenuOpen ? '-rotate-45 -translate-y-2' : ''}`}></span>
                </button>
            </div>

            <div className={`md:hidden absolute top-full left-0 w-full bg-[#050505] border-b border-toro-border shadow-2xl transition-all duration-300 overflow-hidden ${isMenuOpen ? 'max-h-screen opacity-100 py-6' : 'max-h-0 opacity-0 py-0'}`}>
                <div className="flex flex-col items-center gap-6 text-lg font-medium text-zinc-300">
                    <a href="/#problem" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">The Trap</a>
                    <a href="/#features" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">Features</a>
                    <a href="/#pricing" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">Pricing</a>
                    <a href="/#faq" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">FAQ</a>
                    <div className="h-px bg-zinc-800 w-1/3"></div>
                    <div className="flex flex-col gap-4 w-full px-8 items-center">
                        <Link to="/signup" onClick={() => setIsMenuOpen(false)} className="text-sm font-bold text-zinc-400 hover:text-white transition-colors">
                            Sign up
                        </Link>
                        <AuthDropdown />
                    </div>
                </div>
            </div>
        </nav>
    );
};
