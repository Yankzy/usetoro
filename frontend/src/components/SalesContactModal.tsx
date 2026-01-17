import React, { useState } from 'react';
import { Icons } from './Icons';

interface SalesContactModalProps {
    isOpen: boolean;
    tier: string;
    price: string;
    onClose: () => void;
}

export const SalesContactModal = ({ isOpen, tier, price, onClose }: SalesContactModalProps) => {
    if (!isOpen) return null;

    const [formData, setFormData] = useState({ name: '', email: '', company: '', reason: '' });
    const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');

    // --- INSTRUCTIONS ---
    // Sent to Sheet2
    const WEBHOOK_URL = "https://script.google.com/macros/s/AKfycbyJVlAXnOsvYkarrZ9oWV5yEHpn0SgmSap2D5WvWyYEG6UkbrNG1DptpLqHDtlhKL1_PA/exec";
    const TAB_NAME = "Sheet2";

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setStatus('loading');

        try {
            await fetch(WEBHOOK_URL, {
                method: 'POST',
                mode: 'no-cors',
                headers: { 'Content-Type': 'text/plain' },
                body: JSON.stringify({ ...formData, tier, price, sheetName: TAB_NAME })
            });
            setStatus('success');
            setTimeout(() => {
                onClose();
                setStatus('idle');
                setFormData({ name: '', email: '', company: '', reason: '' });
            }, 2000);
        } catch (error) {
            console.error(error);
            setStatus('error');
        }
    };

    return (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
            <div className="absolute inset-0 bg-black/80 backdrop-blur-sm" onClick={onClose}></div>
            <div className="relative bg-[#0A0A0A] border border-toro-border rounded-xl w-full max-w-lg p-6 shadow-2xl animate-[scaleIn_0.2s_ease-out]">
                <button onClick={onClose} className="absolute top-4 right-4 text-zinc-500 hover:text-white transition-colors">
                    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                </button>

                {status === 'success' ? (
                    <div className="text-center py-12">
                        <div className="w-16 h-16 bg-green-500/10 rounded-full flex items-center justify-center mx-auto mb-4">
                            <Icons.Check className="w-8 h-8 text-green-500" />
                        </div>
                        <h3 className="text-xl font-bold mb-2">Message Sent</h3>
                        <p className="text-zinc-400">We'll be in touch shortly.</p>
                    </div>
                ) : (
                    <>
                        <h3 className="text-xl font-bold mb-1">Contact Sales</h3>
                        <p className="text-zinc-500 text-sm mb-6">Tell us about your requirements.</p>

                        <form onSubmit={handleSubmit} className="space-y-4">
                            <div>
                                <label className="block text-xs font-mono text-zinc-500 mb-1.5 uppercase">Name</label>
                                <input
                                    required
                                    type="text"
                                    className="w-full bg-zinc-900/50 border border-zinc-800 rounded-lg px-3 py-2 text-white focus:outline-none focus:border-green-500 transition-colors"
                                    value={formData.name}
                                    onChange={e => setFormData({ ...formData, name: e.target.value })}
                                />
                            </div>
                            <div>
                                <label className="block text-xs font-mono text-zinc-500 mb-1.5 uppercase">Work Email</label>
                                <input
                                    required
                                    type="email"
                                    className="w-full bg-zinc-900/50 border border-zinc-800 rounded-lg px-3 py-2 text-white focus:outline-none focus:border-green-500 transition-colors"
                                    value={formData.email}
                                    onChange={e => setFormData({ ...formData, email: e.target.value })}
                                />
                            </div>
                            <div>
                                <label className="block text-xs font-mono text-zinc-500 mb-1.5 uppercase">Company</label>
                                <input
                                    required
                                    type="text"
                                    className="w-full bg-zinc-900/50 border border-zinc-800 rounded-lg px-3 py-2 text-white focus:outline-none focus:border-green-500 transition-colors"
                                    value={formData.company}
                                    onChange={e => setFormData({ ...formData, company: e.target.value })}
                                />
                            </div>
                            <div>
                                <label className="block text-xs font-mono text-zinc-500 mb-1.5 uppercase">Describe your problem</label>
                                <textarea
                                    required
                                    rows={3}
                                    className="w-full bg-zinc-900/50 border border-zinc-800 rounded-lg px-3 py-2 text-white focus:outline-none focus:border-green-500 transition-colors resize-none"
                                    value={formData.reason}
                                    onChange={e => setFormData({ ...formData, reason: e.target.value })}
                                ></textarea>
                            </div>
                            <button
                                type="submit"
                                disabled={status === 'loading'}
                                className="w-full bg-white text-black font-semibold py-3 rounded-lg hover:bg-gray-200 transition-colors disabled:opacity-50 mt-2"
                            >
                                {status === 'loading' ? 'Sending...' : 'Send Request'}
                            </button>
                        </form>
                    </>
                )}
            </div>
        </div>
    );
};
