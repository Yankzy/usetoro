import os

def create_minimal_pdf(filename, text_content):
    lines = text_content.strip().split('\n')
    text_stream = ""
    for line in lines:
        text_stream += f"({line}) Tj\n0 -14 Td\n"

    stream = f"""BT
/F1 10 Tf
50 730 Td
{text_stream}
ET"""
    
    stream_len = len(stream)
    
    pdf_content = f"""%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>
endobj
4 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj
5 0 obj
<< /Length {stream_len} >>
stream
{stream}
endstream
endobj
xref
0 6
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
0000000227 00000 n 
0000000299 00000 n 
trailer
<< /Size 6 /Root 1 0 R >>
startxref
{349 + stream_len}
%%EOF"""

    with open(filename, 'w') as f:
        f.write(pdf_content)
    print(f"Created {filename}")

if __name__ == "__main__":
    dir_path = os.path.dirname(os.path.realpath(__file__))
    create_minimal_pdf(os.path.join(dir_path, "releve_bancaire.pdf"), """ATTIJARIWAFA BANK - Releve de Compte
Client: Atlas Office Solutions SARL
ICE: 001234567000089
Compte N: 007810000012345678901234
Periode: 01/07/2026 au 31/07/2026

Date       Libelle                                    Debit       Credit      Solde
01/07/2026 Solde Precedent                                                    150,000.00
02/07/2026 Loyer Juil. - Soc. Immobiliere Anfa         12,000.00              138,000.00
05/07/2026 Virement Client ABC Construction SARL                   25,000.00  163,000.00
08/07/2026 Paiement CB Station Afriquia Oasis             850.00              162,150.00
10/07/2026 Achats Fournitures Marjane Business HQ       2,400.00              159,750.00
12/07/2026 Virement Client XYZ Industrie SA                        42,000.00  201,750.00
15/07/2026 Prelevement Facture Orange Maroc SA          1,200.00              200,550.00
18/07/2026 Prelevement Mensuel CNSS                     6,800.00              193,750.00
20/07/2026 Prelevement Facture Lydec Casablanca         1,450.00              192,300.00
22/07/2026 Honoraires Cabinet Comptable El Fassi        3,500.00              188,800.00
25/07/2026 Virement Recu Technopark IT Solutions                   18,500.00  207,300.00
27/07/2026 Prelevement Maroc Telecom (IAM)                950.00              206,350.00
28/07/2026 Paiement Imprimerie Moderne SARL             1,800.00              204,550.00
30/07/2026 Frais Tenue de Compte Attijariwafa Bank        165.00              204,385.00
31/07/2026 Solde Fin de Mois                                                  204,385.00
""")

    create_minimal_pdf(os.path.join(dir_path, "facture.pdf"), """FACTURE N F2026-0712
Orange Maroc SA
Adresse: Boulevard Moulay Ismail, Casablanca, Maroc
ICE: 001524311000045

Client: Atlas Office Solutions SARL
ICE Client: 001234567000089
Date: 15/07/2026

Designation                     Qte     PU (MAD)        HT (MAD)
Abonnement Telecom Business     1       1,000.00        1,000.00

Total HT:   1,000.00 MAD
TVA (20%):    200.00 MAD
Total TTC:  1,200.00 MAD
""")
