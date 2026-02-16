package paillier

import (
	"crypto/rand"
	"errors"
	"math/big"
)

// PrivateKey represents a Paillier private key.
type PrivateKey struct {
	PublicKey
	Lambda *big.Int
	Mu     *big.Int
}

// PublicKey represents a Paillier public key.
type PublicKey struct {
	N  *big.Int
	N2 *big.Int // N^2, cached
	G  *big.Int
}

var one = big.NewInt(1)

// GenerateKey generates a Paillier key pair with the given bit length.
func GenerateKey(bits int) (*PrivateKey, error) {
	p, err := rand.Prime(rand.Reader, bits/2)
	if err != nil {
		return nil, err
	}

	q, err := rand.Prime(rand.Reader, bits/2)
	if err != nil {
		return nil, err
	}

	n := new(big.Int).Mul(p, q)
	n2 := new(big.Int).Mul(n, n)

	// g = n + 1 is a standard optimization
	g := new(big.Int).Add(n, one)

	// lambda = lcm(p-1, q-1) = (p-1)*(q-1) / gcd(p-1, q-1)
	pMinus1 := new(big.Int).Sub(p, one)
	qMinus1 := new(big.Int).Sub(q, one)
	lambda := new(big.Int).Mul(pMinus1, qMinus1)
	gcd := new(big.Int).GCD(nil, nil, pMinus1, qMinus1)
	lambda.Div(lambda, gcd)

	// mu = L(g^lambda mod n^2)^-1 mod n
	// With g = n+1, L(g^lambda mod n^2) simplifies to lambda (conceptually) or easily computed.
	// Actually, strict Paillier: mu = (L(g^lambda mod n^2))^-1 mod n.
	// L(u) = (u-1)/n
	
	u := new(big.Int).Exp(g, lambda, n2)
	l := L(u, n)
	mu := new(big.Int).ModInverse(l, n)

	if mu == nil {
		return nil, errors.New("failed to generate inverse for mu")
	}

	return &PrivateKey{
		PublicKey: PublicKey{
			N:  n,
			N2: n2,
			G:  g,
		},
		Lambda: lambda,
		Mu:     mu,
	}, nil
}

// L function: L(u) = (u - 1) / n
func L(u, n *big.Int) *big.Int {
	t := new(big.Int).Sub(u, one)
	t.Div(t, n)
	return t
}

// Encrypt encrypts a message m into ciphertext c.
func (pub *PublicKey) Encrypt(m *big.Int) (*big.Int, error) {
	if m.Cmp(pub.N) >= 0 {
		return nil, errors.New("message too large for public key")
	}

	r, err := rand.Int(rand.Reader, pub.N)
	if err != nil {
		return nil, err
	}

	// c = g^m * r^n mod n^2
	gm := new(big.Int).Exp(pub.G, m, pub.N2)
	rn := new(big.Int).Exp(r, pub.N, pub.N2)
	c := new(big.Int).Mul(gm, rn)
	c.Mod(c, pub.N2)

	return c, nil
}

// Decrypt decrypts a ciphertext c into message m.
func (priv *PrivateKey) Decrypt(c *big.Int) (*big.Int, error) {
	// m = L(c^lambda mod n^2) * mu mod n
	u := new(big.Int).Exp(c, priv.Lambda, priv.N2)
	l := L(u, priv.N)
	m := new(big.Int).Mul(l, priv.Mu)
	m.Mod(m, priv.N)
	return m, nil
}

// Add computes E(m1 + m2) given E(m1) and E(m2).
// c_sum = c1 * c2 mod n^2
func (pub *PublicKey) Add(c1, c2 *big.Int) *big.Int {
	res := new(big.Int).Mul(c1, c2)
	res.Mod(res, pub.N2)
	return res
}

// AddPlain computes E(m + k) given E(m) and plain k.
// c_new = c * g^k mod n^2
func (pub *PublicKey) AddPlain(c *big.Int, k *big.Int) *big.Int {
	gk := new(big.Int).Exp(pub.G, k, pub.N2)
	res := new(big.Int).Mul(c, gk)
	res.Mod(res, pub.N2)
	return res
}

// MulPlain computes E(m * k) given E(m) and plain k.
// c_new = c^k mod n^2
func (pub *PublicKey) MulPlain(c *big.Int, k *big.Int) *big.Int {
	res := new(big.Int).Exp(c, k, pub.N2)
	return res
}
