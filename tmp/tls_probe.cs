using System;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Cryptography.X509Certificates;

var client = new TcpClient();
client.ReceiveTimeout = 10000;
client.SendTimeout = 10000;
client.Connect("103.236.92.32", 443);
Console.WriteLine("TCP_CONNECTED=true");
var ssl = new SslStream(client.GetStream(), false, (sender, cert, chain, errors) => true);
try {
    ssl.AuthenticateAsClient("103.236.92.32");
    Console.WriteLine("TLS_OK=true");
    Console.WriteLine($"TLS_PROTOCOL={ssl.SslProtocol}");
    Console.WriteLine($"TLS_CIPHER={ssl.CipherAlgorithm}");
    var cert = new X509Certificate2(ssl.RemoteCertificate);
    Console.WriteLine($"CERT_SUBJECT={cert.Subject}");
    Console.WriteLine($"CERT_ISSUER={cert.Issuer}");
} catch (Exception ex) {
    Console.WriteLine("TLS_OK=false");
    Console.WriteLine(ex.GetType().FullName);
    Console.WriteLine(ex.Message);
}
ssl.Dispose();
client.Close();
