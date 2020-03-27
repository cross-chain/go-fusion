package state

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"sync"

	"github.com/FusionFoundation/go-fusion/common"
	"github.com/FusionFoundation/go-fusion/crypto"
	"github.com/FusionFoundation/go-fusion/log"
	"github.com/FusionFoundation/go-fusion/rlp"
)

//------------------------ StateDB -------------------------------------

type CachedTickets struct {
	hash    common.Hash
	tickets common.TicketsDataSlice
}

const maxCachedTicketsCount = 101

type CachedTicketSlice struct {
	tickets [maxCachedTicketsCount]CachedTickets
	start   int64
	end     int64
	rwlock  sync.RWMutex
}

var cachedTicketSlice = CachedTicketSlice{
	tickets: [maxCachedTicketsCount]CachedTickets{},
	start:   0,
	end:     0,
}

func (cts *CachedTicketSlice) Add(hash common.Hash, tickets common.TicketsDataSlice) {
	if cts.Get(hash) != nil {
		return
	}

	elem := CachedTickets{
		hash:    hash,
		tickets: tickets.DeepCopy(),
	}

	cts.rwlock.Lock()
	defer cts.rwlock.Unlock()

	cts.tickets[cts.end] = elem
	cts.end = (cts.end + 1) % maxCachedTicketsCount
	if cts.end == cts.start {
		cts.start = (cts.start + 1) % maxCachedTicketsCount
	}
}

func (cts *CachedTicketSlice) Get(hash common.Hash) common.TicketsDataSlice {
	if hash == (common.Hash{}) {
		return common.TicketsDataSlice{}
	}

	cts.rwlock.RLock()
	defer cts.rwlock.RUnlock()

	for i := cts.start; i != cts.end; i = (i + 1) % maxCachedTicketsCount {
		v := cts.tickets[i]
		if v.hash == hash {
			return v.tickets
		}
	}
	return nil
}

func GetCachedTickets(hash common.Hash) common.TicketsDataSlice {
	return cachedTicketSlice.Get(hash)
}

func calcTicketsStorageData(tickets common.TicketsDataSlice) ([]byte, error) {
	blob, err := rlp.EncodeToBytes(&tickets)
	if err != nil {
		return nil, fmt.Errorf("Unable to encode tickets. err: %v", err)
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(blob); err != nil {
		return nil, fmt.Errorf("Unable to zip tickets data")
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("Unable to zip tickets")
	}
	data := buf.Bytes()
	return data, nil
}

func AddCachedTickets(hash common.Hash, tickets common.TicketsDataSlice) error {
	data, err := calcTicketsStorageData(tickets)
	if err != nil {
		return fmt.Errorf("AddCachedTickets: %v", err)
	}
	if hash != crypto.Keccak256Hash(data) {
		return fmt.Errorf("AddCachedTickets: hash mismatch")
	}
	cachedTicketSlice.Add(hash, tickets)
	return nil
}

// CreateAccount explicitly creates a state object. If a state object with the address
// already exists the balance is carried over to the new account.
//
// CreateAccount is called during the EVM CREATE operation. The situation might arise that
// a contract does the following:
//
//   1. sends funds to sha(account ++ (nonce + 1))
//   2. tx_create(sha(account ++ nonce)) (note that this gets the address of 1)
//
// Carrying over the balance ensures that Ether doesn't disappear.
func (s *StateDB) CreateAccount(addr common.Address) {
	newObj, prev := s.createObject(addr)
	if prev != nil {
		for i, v := range prev.data.BalancesVal {
			newObj.setBalance(prev.data.BalancesHash[i], v)
		}
		for i, v := range prev.data.TimeLockBalancesVal {
			newObj.setTimeLockBalance(prev.data.TimeLockBalancesHash[i], v)
		}
	}
}

// AddBalance adds amount to the account associated with addr.
func (s *StateDB) AddBalance(addr common.Address, assetID common.Hash, amount *big.Int) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.AddBalance(assetID, amount)
	}
}

// SubBalance subtracts amount from the account associated with addr.
func (s *StateDB) SubBalance(addr common.Address, assetID common.Hash, amount *big.Int) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SubBalance(assetID, amount)
	}
}

func (s *StateDB) SetBalance(addr common.Address, assetID common.Hash, amount *big.Int) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetBalance(assetID, amount)
	}
}

func (s *StateDB) GetAllBalances(addr common.Address) map[common.Hash]string {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.CopyBalances()
	}
	return make(map[common.Hash]string)
}

// Retrieve the balance from the given address or 0 if object not found
func (s *StateDB) GetBalance(assetID common.Hash, addr common.Address) *big.Int {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.Balance(assetID)
	}
	return common.Big0
}

func (s *StateDB) AddTimeLockBalance(addr common.Address, assetID common.Hash, amount *common.TimeLock, blockNumber *big.Int, timestamp uint64) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.AddTimeLockBalance(assetID, amount, blockNumber, timestamp)
	}
}

func (s *StateDB) SubTimeLockBalance(addr common.Address, assetID common.Hash, amount *common.TimeLock, blockNumber *big.Int, timestamp uint64) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SubTimeLockBalance(assetID, amount, blockNumber, timestamp)
	}
}

func (s *StateDB) SetTimeLockBalance(addr common.Address, assetID common.Hash, amount *common.TimeLock) {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		stateObject.SetTimeLockBalance(assetID, amount)
	}
}

func (s *StateDB) GetTimeLockBalance(assetID common.Hash, addr common.Address) *common.TimeLock {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.TimeLockBalance(assetID)
	}
	return new(common.TimeLock)
}

func (s *StateDB) GetAllTimeLockBalances(addr common.Address) map[common.Hash]*common.TimeLock {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.CopyTimeLockBalances()
	}
	return make(map[common.Hash]*common.TimeLock)
}

func (s *StateDB) SetData(addr common.Address, value []byte) common.Hash {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		hash := crypto.Keccak256Hash(value)
		stateObject.SetCode(hash, value)
		return hash
	}
	return common.Hash{}
}

func (s *StateDB) GetData(addr common.Address) []byte {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.Code(s.db)
	}
	return nil
}

func (s *StateDB) GetDataHash(addr common.Address) common.Hash {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return common.BytesToHash(stateObject.CodeHash())
	}
	return common.Hash{}
}

// IsTicketExist wacom
func (s *StateDB) IsTicketExist(id common.Hash) bool {
	tickets, err := s.AllTickets()
	if err != nil {
		log.Error("IsTicketExist unable to retrieve all tickets")
		return false
	}

	_, err = tickets.Get(id)
	return err == nil
}

// GetTicket wacom
func (s *StateDB) GetTicket(id common.Hash) (*common.Ticket, error) {
	tickets, err := s.AllTickets()
	if err != nil {
		log.Error("GetTicket unable to retrieve all tickets")
		return nil, fmt.Errorf("GetTicket error: %v", err)
	}
	return tickets.Get(id)
}

// AllTickets wacom
func (s *StateDB) AllTickets() (common.TicketsDataSlice, error) {
	if len(s.tickets) != 0 {
		return s.tickets, nil
	}

	key := s.ticketsHash
	ts := cachedTicketSlice.Get(key)
	if ts != nil {
		s.tickets = ts.DeepCopy()
		return s.tickets, nil
	}

	s.rwlock.RLock()
	defer s.rwlock.RUnlock()

	blob := s.GetData(common.TicketKeyAddress)
	if len(blob) == 0 {
		return common.TicketsDataSlice{}, s.Error()
	}

	gz, err := gzip.NewReader(bytes.NewBuffer(blob))
	if err != nil {
		return nil, fmt.Errorf("Read tickets zip data: %v", err)
	}
	var buf bytes.Buffer
	if _, err = io.Copy(&buf, gz); err != nil {
		return nil, fmt.Errorf("Copy tickets zip data: %v", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("Close read zip tickets: %v", err)
	}
	data := buf.Bytes()

	var tickets common.TicketsDataSlice
	if err := rlp.DecodeBytes(data, &tickets); err != nil {
		log.Error("Unable to decode tickets")
		return nil, fmt.Errorf("Unable to decode tickets, err: %v", err)
	}
	s.tickets = tickets
	cachedTicketSlice.Add(key, s.tickets)
	return s.tickets, nil
}

// AddTicket wacom
func (s *StateDB) AddTicket(ticket common.Ticket) error {
	tickets, err := s.AllTickets()
	if err != nil {
		return fmt.Errorf("AddTicket error: %v", err)
	}
	tickets, err = tickets.AddTicket(&ticket)
	if err != nil {
		return fmt.Errorf("AddTicket error: %v", err)
	}
	s.tickets = tickets
	return nil
}

// RemoveTicket wacom
func (s *StateDB) RemoveTicket(id common.Hash) error {
	tickets, err := s.AllTickets()
	if err != nil {
		return fmt.Errorf("RemoveTicket error: %v", err)
	}
	tickets, err = tickets.RemoveTicket(id)
	if err != nil {
		return fmt.Errorf("RemoveTicket error: %v", err)
	}
	s.tickets = tickets
	return nil
}

func (s *StateDB) TotalNumberOfTickets() uint64 {
	s.rwlock.RLock()
	defer s.rwlock.RUnlock()

	return s.tickets.NumberOfTickets()
}

func (s *StateDB) UpdateTickets(blockNumber *big.Int, timestamp uint64) (common.Hash, error) {
	s.rwlock.Lock()
	defer s.rwlock.Unlock()

	tickets := s.tickets
	tickets, err := tickets.ClearExpiredTickets(timestamp)
	if err != nil {
		return common.Hash{}, fmt.Errorf("UpdateTickets: %v", err)
	}
	s.tickets = tickets

	data, err := calcTicketsStorageData(s.tickets)
	if err != nil {
		return common.Hash{}, fmt.Errorf("UpdateTickets: %v", err)
	}

	hash := s.SetData(common.TicketKeyAddress, data)
	cachedTicketSlice.Add(hash, s.tickets)
	return hash, nil
}

func (s *StateDB) ClearTickets(from, to common.Address, blockNumber *big.Int, timestamp uint64) {
	tickets, err := s.AllTickets()
	if err != nil {
		return
	}
	for i, v := range tickets {
		if v.Owner != from {
			continue
		}
		for _, ticket := range v.Tickets {
			if ticket.ExpireTime <= timestamp {
				continue
			}
			value := common.NewTimeLock(&common.TimeLockItem{
				StartTime: ticket.StartTime,
				EndTime:   ticket.ExpireTime,
				Value:     ticket.Value(),
			})
			s.AddTimeLockBalance(to, common.SystemAssetID, value, blockNumber, timestamp)
		}
		tickets = append(tickets[:i], tickets[i+1:]...)
		s.tickets = tickets
		break
	}
}

func (s *StateDB) TransferAll(from, to common.Address, blockNumber *big.Int, timestamp uint64) {
	fromObject := s.getStateObject(from)
	if fromObject == nil {
		return
	}

	// remove tickets
	s.ClearTickets(from, to, blockNumber, timestamp)

	// burn notation
	s.BurnNotation(from)

	// transfer all balances
	for i, v := range fromObject.data.BalancesVal {
		k := fromObject.data.BalancesHash[i]
		fromObject.SetBalance(k, new(big.Int))
		s.AddBalance(to, k, v)
	}

	// transfer all timelock balances
	for i, v := range fromObject.data.TimeLockBalancesVal {
		k := fromObject.data.TimeLockBalancesHash[i]
		fromObject.SetTimeLockBalance(k, new(common.TimeLock))
		s.AddTimeLockBalance(to, k, v, blockNumber, timestamp)
	}
}

// GetNotation wacom
func (s *StateDB) GetNotation(addr common.Address) uint64 {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		return stateObject.Notation()
	}
	return 0
}

// AllNotation wacom
func (s *StateDB) AllNotation() ([]common.Address, error) {
	return nil, fmt.Errorf("AllNotations has been depreciated please use api.fusionnetwork.io")
}

// GenNotation wacom
func (s *StateDB) GenNotation(addr common.Address) error {
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		if n := s.GetNotation(addr); n != 0 {
			return fmt.Errorf("Account %s has a notation:%d", addr.String(), n)
		}
		// get last notation value
		nextNotation, err := s.GetNotationCount()
		nextNotation++
		if err != nil {
			log.Error("GenNotation: Unable to get next notation value")
			return err
		}
		newNotation := s.CalcNotationDisplay(nextNotation)
		s.setNotationCount(nextNotation)
		s.setNotationToAddressLookup(newNotation, addr)
		stateObject.SetNotation(newNotation)
		return nil
	}
	return nil
}

func (s *StateDB) BurnNotation(addr common.Address) {
	stateObject := s.getStateObject(addr)
	if stateObject != nil {
		notation := stateObject.Notation()
		if notation != 0 {
			s.setNotationToAddressLookup(notation, common.Address{})
			stateObject.SetNotation(0)
		}
	}
}

type notationPersist struct {
	Deleted bool
	Count   uint64
	Address common.Address
}

func (s *StateDB) GetNotationCount() (uint64, error) {
	data := s.GetStructData(common.NotationKeyAddress, common.NotationKeyAddress.Bytes())
	if len(data) == 0 || data == nil {
		return 0, nil // not created yet
	}
	var np notationPersist
	rlp.DecodeBytes(data, &np)
	return np.Count, nil
}

func (s *StateDB) setNotationCount(newCount uint64) error {
	np := notationPersist{
		Count: newCount,
	}
	data, err := rlp.EncodeToBytes(&np)
	if err != nil {
		return err
	}
	s.SetStructData(common.NotationKeyAddress, common.NotationKeyAddress.Bytes(), data)
	return nil
}

func (s *StateDB) setNotationToAddressLookup(notation uint64, address common.Address) error {
	np := notationPersist{
		Count:   notation,
		Address: address,
	}
	data, err := rlp.EncodeToBytes(&np)
	if err != nil {
		return err
	}
	buf := make([]byte, binary.MaxVarintLen64)
	binary.PutUvarint(buf, notation)
	s.SetStructData(common.NotationKeyAddress, buf, data)
	return nil
}

// GetAddressByNotation wacom
func (s *StateDB) GetAddressByNotation(notation uint64) (common.Address, error) {
	buf := make([]byte, binary.MaxVarintLen64)
	binary.PutUvarint(buf, notation)
	data := s.GetStructData(common.NotationKeyAddress, buf)
	if len(data) == 0 || data == nil {
		return common.Address{}, fmt.Errorf("notation %v does not exist", notation)
	}
	var np notationPersist
	err := rlp.DecodeBytes(data, &np)
	if err != nil {
		return common.Address{}, err
	}
	if np.Deleted || np.Address == (common.Address{}) {
		return common.Address{}, fmt.Errorf("notation was deleted")
	}
	return np.Address, nil
}

// TransferNotation wacom
func (s *StateDB) TransferNotation(notation uint64, from common.Address, to common.Address) error {
	stateObjectFrom := s.GetOrNewStateObject(from)
	if stateObjectFrom == nil {
		return fmt.Errorf("Unable to get from address")
	}
	stateObjectTo := s.GetOrNewStateObject(to)
	if stateObjectTo == nil {
		return fmt.Errorf("Unable to get to address")
	}
	address, err := s.GetAddressByNotation(notation)
	if err != nil {
		return err
	}
	if address != from {
		return fmt.Errorf("This notation is not the from address")
	}
	// reset the notation
	oldNotationTo := stateObjectTo.Notation()
	if oldNotationTo != 0 {
		// need to clear notation to address
		// user should transfer an old notation or can burn it like this
		s.setNotationToAddressLookup(oldNotationTo, common.Address{})
	}
	s.setNotationToAddressLookup(notation, to)
	stateObjectTo.SetNotation(notation)
	stateObjectFrom.SetNotation(0)
	return nil
}

// CalcNotationDisplay wacom
func (s *StateDB) CalcNotationDisplay(notation uint64) uint64 {
	if notation == 0 {
		return notation
	}
	check := (notation ^ 8192 ^ 13) % 100
	return (notation*100 + check)
}

// AllAssets wacom
func (s *StateDB) AllAssets() (map[common.Hash]common.Asset, error) {
	return nil, fmt.Errorf("All assets has been depreciated, use api.fusionnetwork.io")
}

type assetPersist struct {
	Deleted bool // if true swap was recalled and should not be returned
	Asset   common.Asset
}

// GetAsset wacom
func (s *StateDB) GetAsset(assetID common.Hash) (common.Asset, error) {
	data := s.GetStructData(common.AssetKeyAddress, assetID.Bytes())
	var asset assetPersist
	if len(data) == 0 || data == nil {
		return common.Asset{}, fmt.Errorf("asset not found")
	}
	rlp.DecodeBytes(data, &asset)
	if asset.Deleted {
		return common.Asset{}, fmt.Errorf("asset deleted")
	}
	return asset.Asset, nil
}

// GenAsset wacom
func (s *StateDB) GenAsset(asset common.Asset) error {
	_, err := s.GetAsset(asset.ID)
	if err == nil {
		return fmt.Errorf("%s asset exists", asset.ID.String())
	}
	assetToSave := assetPersist{
		Deleted: false,
		Asset:   asset,
	}
	data, err := rlp.EncodeToBytes(&assetToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.AssetKeyAddress, asset.ID.Bytes(), data)
	return nil
}

// UpdateAsset wacom
func (s *StateDB) UpdateAsset(asset common.Asset) error {
	/** to update a asset we just overwrite it
	 */
	assetToSave := assetPersist{
		Deleted: false,
		Asset:   asset,
	}
	data, err := rlp.EncodeToBytes(&assetToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.AssetKeyAddress, asset.ID.Bytes(), data)
	return nil
}

// AllSwaps wacom
func (s *StateDB) AllSwaps() (map[common.Hash]common.Swap, error) {
	return nil, fmt.Errorf("AllSwaps has been depreciated please use api.fusionnetwork.io")
}

/** swaps
*
 */
type swapPersist struct {
	Deleted bool // if true swap was recalled and should not be returned
	Swap    common.Swap
}

// GetSwap wacom
func (s *StateDB) GetSwap(swapID common.Hash) (common.Swap, error) {
	data := s.GetStructData(common.SwapKeyAddress, swapID.Bytes())
	var swap swapPersist
	if len(data) == 0 || data == nil {
		return common.Swap{}, fmt.Errorf("swap not found")
	}
	rlp.DecodeBytes(data, &swap)
	if swap.Deleted {
		return common.Swap{}, fmt.Errorf("swap deleted")
	}
	return swap.Swap, nil
}

// AddSwap wacom
func (s *StateDB) AddSwap(swap common.Swap) error {
	_, err := s.GetSwap(swap.ID)
	if err == nil {
		return fmt.Errorf("%s Swap exists", swap.ID.String())
	}
	swapToSave := swapPersist{
		Deleted: false,
		Swap:    swap,
	}
	data, err := rlp.EncodeToBytes(&swapToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.SwapKeyAddress, swap.ID.Bytes(), data)
	return nil
}

// UpdateSwap wacom
func (s *StateDB) UpdateSwap(swap common.Swap) error {
	/** to update a swap we just overwrite it
	 */
	swapToSave := swapPersist{
		Deleted: false,
		Swap:    swap,
	}
	data, err := rlp.EncodeToBytes(&swapToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.SwapKeyAddress, swap.ID.Bytes(), data)
	return nil
}

// RemoveSwap wacom
func (s *StateDB) RemoveSwap(id common.Hash) error {
	swapFound, err := s.GetSwap(id)
	if err != nil {
		return fmt.Errorf("%s Swap not found ", id.String())
	}

	swapToSave := swapPersist{
		Deleted: true,
		Swap:    swapFound,
	}
	data, err := rlp.EncodeToBytes(&swapToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.SwapKeyAddress, id.Bytes(), data)
	return nil
}

/** swaps
*
 */
type multiSwapPersist struct {
	Deleted bool // if true swap was recalled and should not be returned
	Swap    common.MultiSwap
}

// GetMultiSwap wacom
func (s *StateDB) GetMultiSwap(swapID common.Hash) (common.MultiSwap, error) {
	data := s.GetStructData(common.MultiSwapKeyAddress, swapID.Bytes())
	var swap multiSwapPersist
	if len(data) == 0 || data == nil {
		return common.MultiSwap{}, fmt.Errorf("multi swap not found")
	}
	rlp.DecodeBytes(data, &swap)
	if swap.Deleted {
		return common.MultiSwap{}, fmt.Errorf("multi swap deleted")
	}
	return swap.Swap, nil
}

// AddMultiSwap wacom
func (s *StateDB) AddMultiSwap(swap common.MultiSwap) error {
	_, err := s.GetMultiSwap(swap.ID)
	if err == nil {
		return fmt.Errorf("%s Multi Swap exists", swap.ID.String())
	}
	swapToSave := multiSwapPersist{
		Deleted: false,
		Swap:    swap,
	}
	data, err := rlp.EncodeToBytes(&swapToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.MultiSwapKeyAddress, swap.ID.Bytes(), data)
	return nil
}

// UpdateMultiSwap wacom
func (s *StateDB) UpdateMultiSwap(swap common.MultiSwap) error {
	/** to update a swap we just overwrite it
	 */
	swapToSave := multiSwapPersist{
		Deleted: false,
		Swap:    swap,
	}
	data, err := rlp.EncodeToBytes(&swapToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.MultiSwapKeyAddress, swap.ID.Bytes(), data)
	return nil
}

// RemoveSwap wacom
func (s *StateDB) RemoveMultiSwap(id common.Hash) error {
	swapFound, err := s.GetMultiSwap(id)
	if err != nil {
		return fmt.Errorf("%s Multi Swap not found ", id.String())
	}

	swapToSave := multiSwapPersist{
		Deleted: true,
		Swap:    swapFound,
	}
	data, err := rlp.EncodeToBytes(&swapToSave)
	if err != nil {
		return err
	}
	s.SetStructData(common.MultiSwapKeyAddress, id.Bytes(), data)
	return nil
}

/** ReportIllegal
 */

// GetReport wacom
func (s *StateDB) IsReportExist(report []byte) bool {
	hash := crypto.Keccak256Hash(report)
	data := s.GetStructData(common.ReportKeyAddress, hash.Bytes())
	return len(data) > 0
}

// AddReport wacom
func (s *StateDB) AddReport(report []byte) error {
	if s.IsReportExist(report) {
		return fmt.Errorf("AddReport error: report exists")
	}
	hash := crypto.Keccak256Hash(report)
	s.SetStructData(common.ReportKeyAddress, hash.Bytes(), report)
	return nil
}

// GetStructData wacom
func (s *StateDB) GetStructData(addr common.Address, key []byte) []byte {
	if key == nil {
		return nil
	}
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		keyHash := crypto.Keccak256Hash(key)
		keyIndex := new(big.Int)
		keyIndex.SetBytes(keyHash[:])
		info := stateObject.GetState(s.db, keyHash)
		size := common.BytesToInt(info[0:4])
		length := common.BytesToInt(info[common.HashLength/2 : common.HashLength/2+4])
		data := make([]byte, size)
		for i := 0; i < length; i++ {
			tempIndex := big.NewInt(int64(i))
			tempKey := crypto.Keccak256Hash(tempIndex.Bytes(), keyIndex.Bytes())
			tempData := stateObject.GetState(s.db, tempKey)
			start := i * common.HashLength
			end := start + common.HashLength
			if end > size {
				end = size
			}
			copy(data[start:end], tempData[common.HashLength-end+start:])
		}
		return data
	}

	return nil
}

// SetStructData wacom
func (s *StateDB) SetStructData(addr common.Address, key, value []byte) {
	if key == nil || value == nil {
		return
	}
	stateObject := s.GetOrNewStateObject(addr)
	if stateObject != nil {
		size := len(value)
		length := size / common.HashLength
		if size%common.HashLength != 0 {
			length++
		}
		info := common.Hash{}
		copy(info[0:], common.IntToBytes(size))
		copy(info[common.HashLength/2:], common.IntToBytes(length))
		keyHash := crypto.Keccak256Hash(key)
		keyIndex := new(big.Int)
		keyIndex.SetBytes(keyHash[:])
		stateObject.SetState(s.db, keyHash, info)
		for i := 0; i < length; i++ {
			tempIndex := big.NewInt(int64(i))
			tempKey := crypto.Keccak256Hash(tempIndex.Bytes(), keyIndex.Bytes())
			tempData := common.Hash{}
			start := i * common.HashLength
			end := start + common.HashLength
			if end > size {
				end = size
			}
			tempData.SetBytes(value[start:end])
			stateObject.SetState(s.db, tempKey, tempData)
		}
		stateObject.SetNonce(stateObject.Nonce() + 1)
	}
}

//------------------------ stateObject ----------------------------------

func (acc *Account) GetBalance(assetID common.Hash) *big.Int {
	for i, v := range acc.BalancesHash {
		if v == assetID {
			return acc.BalancesVal[i]
		}
	}
	return common.Big0
}

func (s *stateObject) balanceAssetIndex(assetID common.Hash) int {
	for i, v := range s.data.BalancesHash {
		if v == assetID {
			return i
		}
	}

	s.data.BalancesHash = append(s.data.BalancesHash, assetID)
	s.data.BalancesVal = append(s.data.BalancesVal, new(big.Int))

	return len(s.data.BalancesVal) - 1
}

// AddBalance removes amount from c's balance.
// It is used to add funds to the destination account of a transfer.
func (s *stateObject) AddBalance(assetID common.Hash, amount *big.Int) {
	// EIP158: We must check emptiness for the objects such that the account
	// clearing (0,0,0 objects) can take effect.
	if amount.Sign() == 0 {
		if s.empty() {
			s.touch()
		}

		return
	}
	index := s.balanceAssetIndex(assetID)
	s.SetBalance(assetID, new(big.Int).Add(s.data.BalancesVal[index], amount))
}

// SubBalance removes amount from c's balance.
// It is used to remove funds from the origin account of a transfer.
func (s *stateObject) SubBalance(assetID common.Hash, amount *big.Int) {
	if amount.Sign() == 0 {
		return
	}
	index := s.balanceAssetIndex(assetID)
	s.SetBalance(assetID, new(big.Int).Sub(s.data.BalancesVal[index], amount))
}

func (s *stateObject) SetBalance(assetID common.Hash, amount *big.Int) {
	index := s.balanceAssetIndex(assetID)
	s.db.journal.append(balanceChange{
		account: &s.address,
		assetID: assetID,
		prev:    new(big.Int).Set(s.data.BalancesVal[index]),
	})
	s.setBalance(assetID, amount)
}

func (s *stateObject) setBalance(assetID common.Hash, amount *big.Int) {
	index := s.balanceAssetIndex(assetID)
	s.data.BalancesVal[index] = amount
}

func (c *stateObject) timeLockAssetIndex(assetID common.Hash) int {
	for i, v := range c.data.TimeLockBalancesHash {
		if v == assetID {
			return i
		}
	}

	c.data.TimeLockBalancesHash = append(c.data.TimeLockBalancesHash, assetID)
	c.data.TimeLockBalancesVal = append(c.data.TimeLockBalancesVal, new(common.TimeLock))

	return len(c.data.TimeLockBalancesVal) - 1
}

// AddTimeLockBalance wacom
func (s *stateObject) AddTimeLockBalance(assetID common.Hash, amount *common.TimeLock, blockNumber *big.Int, timestamp uint64) {
	if amount.IsEmpty() {
		if s.empty() {
			s.touch()
		}
		return
	}

	index := s.timeLockAssetIndex(assetID)
	res := s.data.TimeLockBalancesVal[index]
	res = new(common.TimeLock).Add(res, amount)
	if res != nil {
		res = res.ClearExpired(timestamp)
	}

	s.SetTimeLockBalance(assetID, res)
}

// SubTimeLockBalance wacom
func (s *stateObject) SubTimeLockBalance(assetID common.Hash, amount *common.TimeLock, blockNumber *big.Int, timestamp uint64) {
	if amount.IsEmpty() {
		return
	}

	index := s.timeLockAssetIndex(assetID)
	res := s.data.TimeLockBalancesVal[index]
	res = new(common.TimeLock).Sub(res, amount)
	if res != nil {
		res = res.ClearExpired(timestamp)
	}

	s.SetTimeLockBalance(assetID, res)
}

func (s *stateObject) SetTimeLockBalance(assetID common.Hash, amount *common.TimeLock) {
	index := s.timeLockAssetIndex(assetID)
	s.db.journal.append(timeLockBalanceChange{
		account: &s.address,
		assetID: assetID,
		prev:    new(common.TimeLock).Set(s.data.TimeLockBalancesVal[index]),
	})
	s.setTimeLockBalance(assetID, amount)
}

func (s *stateObject) setTimeLockBalance(assetID common.Hash, amount *common.TimeLock) {
	index := s.timeLockAssetIndex(assetID)
	s.data.TimeLockBalancesVal[index] = amount
}

func (s *stateObject) SetNotation(notation uint64) {
	s.db.journal.append(notationChange{
		account: &s.address,
		prev:    s.data.Notaion,
	})
	s.setNotation(notation)
}

func (s *stateObject) setNotation(notation uint64) {
	s.data.Notaion = notation
}

func (s *stateObject) CopyBalances() map[common.Hash]string {
	retBalances := make(map[common.Hash]string)
	for i, v := range s.data.BalancesHash {
		if s.data.BalancesVal[i].Sign() != 0 {
			retBalances[v] = s.data.BalancesVal[i].String()
		}
	}
	return retBalances
}

func (s *stateObject) Balance(assetID common.Hash) *big.Int {
	index := s.balanceAssetIndex(assetID)
	return s.data.BalancesVal[index]
}

// do not modify Account data in querying
func (s *stateObject) GetBalance(assetID common.Hash) *big.Int {
	return s.data.GetBalance(assetID)
}

func (s *stateObject) CopyTimeLockBalances() map[common.Hash]*common.TimeLock {
	retBalances := make(map[common.Hash]*common.TimeLock)
	for i, v := range s.data.TimeLockBalancesHash {
		if !s.data.TimeLockBalancesVal[i].IsEmpty() {
			retBalances[v] = s.data.TimeLockBalancesVal[i]
		}
	}
	return retBalances
}

func (s *stateObject) TimeLockBalance(assetID common.Hash) *common.TimeLock {
	index := s.timeLockAssetIndex(assetID)
	return s.data.TimeLockBalancesVal[index]
}

func (s *stateObject) Notation() uint64 {
	return s.data.Notaion
}
